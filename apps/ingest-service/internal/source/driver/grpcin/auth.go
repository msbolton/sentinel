package grpcin

import (
	"context"
	"errors"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/ingesttoken"
)

var (
	errRevoked = errors.New("the ingest token was revoked")
	errMoving  = errors.New("connection moving")
	errExpired = errors.New("the ingest token has expired")
)

type authKey struct{}

// authed is the token a call authenticated with.
type authed struct {
	tok    ingesttoken.Token
	secret string
}

func caller(ctx context.Context) authed {
	a, _ := ctx.Value(authKey{}).(authed)
	return a
}

// peerOf is the sender's ip:port, the key of its row in senders.
func peerOf(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}

func bearer(ctx context.Context) (string, bool) {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		scheme, secret, ok := strings.Cut(strings.TrimSpace(v), " ")
		if ok && strings.EqualFold(scheme, "bearer") && strings.TrimSpace(secret) != "" {
			return strings.TrimSpace(secret), true
		}
	}
	return "", false
}

// authenticate checks a call's token. Until the cache has loaded nothing
// can be checked, so the answer is Unavailable (retry), not Unauthenticated.
func (s *service) authenticate(ctx context.Context) (authed, error) {
	select {
	case <-s.d.tokens.Ready():
	default:
		return authed{}, status.Error(codes.Unavailable, "the ingest service is still loading its tokens: retry")
	}
	secret, ok := bearer(ctx)
	if !ok {
		return authed{}, status.Error(codes.Unauthenticated, "send the connection's ingest token as authorization: Bearer si_…")
	}
	tok, ok := s.d.tokens.Authenticate(secret)
	if !ok {
		return authed{}, status.Error(codes.Unauthenticated, "the ingest token is unknown, revoked or expired")
	}
	if tok.FeedID != s.d.feedID {
		return authed{}, status.Error(codes.PermissionDenied, "the ingest token belongs to another connection")
	}
	return authed{tok: tok, secret: secret}, nil
}

// begin authenticates a call and registers it, so a revocation of its token
// or the end of the run ends it. The token is checked again once the call
// is registered: a revocation between the two checks would otherwise find
// no call to end. end must be called when the call returns.
func (s *service) begin(ctx context.Context) (context.Context, func(), error) {
	a, err := s.authenticate(ctx)
	if err != nil {
		return nil, nil, err
	}
	base, cancel := context.WithCancelCause(context.WithValue(ctx, authKey{}, a))
	cctx, release := base, func() {}
	// The token cache announces a revocation but not an expiry, so an expiry
	// ends the call at its time, an idle stream included.
	if a.tok.ExpiresAt != nil {
		var stop context.CancelFunc
		cctx, stop = context.WithDeadlineCause(base, *a.tok.ExpiresAt, errExpired)
		release = stop
	}
	remove := s.calls.add(a.tok.ID, cancel)
	stopMoving := context.AfterFunc(s.run, func() { cancel(errMoving) })
	end := func() { stopMoving(); remove(); release(); cancel(nil) }
	if _, ok := s.d.tokens.Authenticate(a.secret); !ok {
		end()
		return nil, nil, status.Error(codes.Unauthenticated, "the ingest token was revoked")
	}
	return cctx, end, nil
}

// ended turns a call cut short by a revocation, an expiry or the run's end
// into the status the sender should see. A successful result is never
// rewritten: reports already sunk must not be reported failed.
func ended(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errRevoked):
		return status.Error(codes.Unauthenticated, "the ingest token was revoked")
	case errors.Is(cause, errExpired):
		return status.Error(codes.Unauthenticated, "the ingest token has expired")
	case errors.Is(cause, errMoving):
		return status.Error(codes.Unavailable, "connection moving: reconnect")
	}
	return err
}

func (s *service) unary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	cctx, end, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	resp, err := h(cctx, req)
	return resp, ended(cctx, err)
}

func (s *service) stream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	cctx, end, err := s.begin(ss.Context())
	if err != nil {
		return err
	}
	defer end()
	return ended(cctx, h(srv, &callStream{ServerStream: ss, ctx: cctx}))
}

type callStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (c *callStream) Context() context.Context { return c.ctx }

// calls is every open call by token, so a revocation can end them.
type calls struct {
	mu      sync.Mutex
	next    int
	byToken map[string]map[int]context.CancelCauseFunc
}

func (c *calls) add(tokenID string, cancel context.CancelCauseFunc) (remove func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byToken == nil {
		c.byToken = map[string]map[int]context.CancelCauseFunc{}
	}
	if c.byToken[tokenID] == nil {
		c.byToken[tokenID] = map[int]context.CancelCauseFunc{}
	}
	id := c.next
	c.next++
	c.byToken[tokenID][id] = cancel
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.byToken[tokenID], id)
		if len(c.byToken[tokenID]) == 0 {
			delete(c.byToken, tokenID)
		}
	}
}

// revoke ends every call made with the token. It does not block: it is
// called from the token cache's refresh.
func (c *calls) revoke(tokenID string) {
	c.mu.Lock()
	var cancels []context.CancelCauseFunc
	for _, cancel := range c.byToken[tokenID] {
		cancels = append(cancels, cancel)
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel(errRevoked)
	}
}
