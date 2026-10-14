package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// ErrInvalidToken is returned for any token that fails verification.
var ErrInvalidToken = errors.New("invalid token")

// Verifier validates a raw bearer token and returns the authenticated user.
type Verifier interface {
	Verify(ctx context.Context, rawToken string) (*User, error)
}

// VerifierFunc adapts a function to the Verifier interface.
type VerifierFunc func(ctx context.Context, rawToken string) (*User, error)

// Verify implements Verifier.
func (f VerifierFunc) Verify(ctx context.Context, rawToken string) (*User, error) {
	return f(ctx, rawToken)
}

// keycloakClaims is the subset of a Keycloak access token the gateway reads.
type keycloakClaims struct {
	jwt.RegisteredClaims
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
	ClassificationLevel string `json:"classification_level"`
}

// KeycloakOptions configures a KeycloakVerifier.
type KeycloakOptions struct {
	// Keyfunc resolves the signing key for a token (normally a JWKS lookup).
	Keyfunc jwt.Keyfunc
	// Issuers is the set of accepted `iss` values. Empty disables the check.
	Issuers []string
	// Audience is the required `aud` value. Empty disables the check.
	Audience string
	// ClientID selects which resource_access entry contributes client roles.
	ClientID string
	// Leeway tolerates clock skew on exp/nbf. Defaults to 30 seconds.
	Leeway time.Duration
}

// KeycloakVerifier validates RS256 tokens issued by Keycloak.
type KeycloakVerifier struct {
	keyfunc  jwt.Keyfunc
	issuers  map[string]struct{}
	audience string
	clientID string
	leeway   time.Duration
}

// NewKeycloakVerifier builds a verifier from options.
func NewKeycloakVerifier(o KeycloakOptions) *KeycloakVerifier {
	v := &KeycloakVerifier{
		keyfunc:  o.Keyfunc,
		issuers:  map[string]struct{}{},
		audience: o.Audience,
		clientID: o.ClientID,
		leeway:   o.Leeway,
	}
	for _, iss := range o.Issuers {
		v.issuers[iss] = struct{}{}
	}
	if v.leeway == 0 {
		v.leeway = 30 * time.Second
	}
	return v
}

// Verify implements Verifier.
func (v *KeycloakVerifier) Verify(_ context.Context, rawToken string) (*User, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}

	var claims keycloakClaims
	token, err := jwt.ParseWithClaims(rawToken, &claims, v.keyfunc, opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}
	if len(v.issuers) > 0 {
		if _, ok := v.issuers[claims.Issuer]; !ok {
			return nil, fmt.Errorf("%w: unexpected issuer %q", ErrInvalidToken, claims.Issuer)
		}
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing subject claim", ErrInvalidToken)
	}

	realmRoles := claims.RealmAccess.Roles
	if realmRoles == nil {
		realmRoles = []string{}
	}
	clientRoles := []string{}
	if ra, ok := claims.ResourceAccess[v.clientID]; ok && ra.Roles != nil {
		clientRoles = ra.Roles
	}

	seen := map[string]struct{}{}
	all := make([]string, 0, len(realmRoles)+len(clientRoles))
	for _, r := range append(append([]string{}, realmRoles...), clientRoles...) {
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		all = append(all, r)
	}

	classification := claims.ClassificationLevel
	if classification == "" {
		classification = "UNCLASSIFIED"
	}

	return &User{
		UserID:              claims.Subject,
		Username:            claims.PreferredUsername,
		Email:               claims.Email,
		Name:                claims.Name,
		Roles:               all,
		RealmRoles:          realmRoles,
		ClientRoles:         clientRoles,
		ClassificationLevel: classification,
	}, nil
}

// NewJWKSKeyfunc returns a jwt.Keyfunc backed by a remote JWKS. The JWKS is
// fetched lazily on first use and refreshed in the background, so the gateway
// can start before Keycloak is reachable.
func NewJWKSKeyfunc(jwksURL string, logger *zap.Logger) jwt.Keyfunc {
	var (
		mu sync.Mutex
		kf keyfunc.Keyfunc
	)
	return func(token *jwt.Token) (interface{}, error) {
		mu.Lock()
		if kf == nil {
			k, err := keyfunc.NewDefaultCtx(context.Background(), []string{jwksURL})
			if err != nil {
				mu.Unlock()
				logger.Warn("jwks fetch failed", zap.String("url", jwksURL), zap.Error(err))
				return nil, fmt.Errorf("jwks unavailable: %w", err)
			}
			logger.Info("jwks loaded", zap.String("url", jwksURL))
			kf = k
		}
		k := kf
		mu.Unlock()
		return k.Keyfunc(token)
	}
}
