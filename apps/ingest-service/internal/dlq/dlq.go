// Package dlq reads the dead-letter topic back for the management API: the
// newest payloads a connection's decoder refused, with what the publisher
// recorded about each, so that an operator can see what a sender gets wrong.
//
// The read never joins a consumer group and never commits an offset. It asks
// each partition where it ends, assigns itself the last stretch of it and reads
// that, so nothing it does is visible to the services that consume the topic.
package dlq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	commonkafka "github.com/sentinel/go-common/kafka"
)

const (
	// PayloadMax is how much of a payload a Letter keeps.
	PayloadMax = 1024
	// Deadline bounds one read of the topic, the wait for an earlier read included.
	Deadline = 5 * time.Second

	// pollWait is how long the scan waits for a message at a time. It is short
	// so that a deadline is noticed, not because messages are slow to arrive.
	pollWait = 100 * time.Millisecond
)

// Letter is one dead letter: a payload nothing could decode, and what the
// publisher recorded about it.
type Letter struct {
	// At is when the frame arrived (the received-at header), or when Kafka
	// recorded the message if the header is missing or unreadable. It is UTC.
	At      time.Time
	Reason  string // why it was refused: parse_error, missing_identity, ...
	Error   string // the decoder's error text, which can repeat the payload
	Driver  string
	Decoder string
	Sender  string // who sent it, as the driver names senders; empty if it did not know
	// Size is the length of the whole payload. Payload holds its first
	// PayloadMax bytes, fewer when the cut would split a character.
	Size             int
	Payload          []byte
	PayloadTruncated bool
	Partition        int32
	Offset           int64
}

// Reader returns a connection's most recent dead letters.
type Reader interface {
	// Recent returns up to limit dead letters of the feed, newest first. scanned
	// is how many messages it read to find them, the feed's or not. partial is
	// true when the read stopped at its deadline before it had covered what it
	// meant to, so there may be newer or more letters than it returned.
	Recent(ctx context.Context, feedID string, limit int) (letters []Letter, scanned int, partial bool, err error)
}

// source is the part of a Kafka consumer a scan uses. KafkaReader provides it
// over librdkafka; the tests provide it over a list.
type source interface {
	// Bounds lists the topic's partitions with the offsets each holds.
	Bounds(ctx context.Context) ([]bounds, error)
	// Assign replaces what the consumer reads: each partition from its offset on.
	Assign(from map[int32]int64) error
	// Unassign stops the reading.
	Unassign() error
	// Next waits up to wait for the next message, or for news that a partition
	// has been read to its end. ok is false if neither came in that time.
	Next(wait time.Duration) (ev event, ok bool, err error)
	// LastError is the last error the consumer reported and recovers from by
	// itself since the last Assign (a broker went away, say), or nil. Errors
	// that will not pass fail Next instead.
	LastError() error
	// Close releases the consumer.
	Close() error
}

// bounds are the offsets a partition holds: low is the first one still
// there, high the one the next message will get.
type bounds struct {
	partition int32
	low, high int64
}

// event is a message of the topic, or, with eof set, the news that its
// partition has been read to its end (all the other fields but partition are
// then empty).
type event struct {
	partition int32
	offset    int64
	at        time.Time // when Kafka recorded the message (received-at, if the headers have it, is used instead)
	key       string
	value     []byte
	headers   []header
	eof       bool
}

type header struct {
	key   string
	value []byte
}

// span is what is left to read of one partition: from next, up to but not
// including end, which is where the partition ended when the scan began.
type span struct {
	next, end int64
	// started is set by the first message of the range. Until then only the
	// offset asked for is taken (or the first offset the partition still holds,
	// low, if retention removed the start meanwhile), so that a message left
	// over from an earlier assignment is not taken for the range's first.
	started  bool
	low      int64
	lowKnown bool
}

// scan reads the last maxScan messages of every partition of the topic and
// returns the limit newest of those whose key is feedID. Both the read and the
// wait for each message are bounded by ctx: at its deadline it returns what it
// has, partial; cancelled, it returns the cancellation.
//
// The range read is fixed when the scan begins, so a topic that keeps growing
// cannot keep it going. A partition is finished when its last message of that
// range has been read, when the consumer reports its end (an offset can fail to
// arrive, as a transaction marker does), or when a message past the range
// arrives.
func scan(ctx context.Context, src source, maxScan int64, feedID string, limit int) ([]Letter, int, bool, error) {
	if limit <= 0 {
		return nil, 0, false, nil
	}
	parts, err := src.Bounds(ctx)
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading the partitions of the dead-letter topic: %w", err)
	}
	reading := map[int32]*span{}
	from := map[int32]int64{}
	for _, b := range parts {
		start := max(b.low, b.high-maxScan, 0)
		if start >= b.high {
			continue // nothing there
		}
		reading[b.partition] = &span{next: start, end: b.high}
		from[b.partition] = start
	}
	if len(reading) == 0 {
		return nil, 0, false, nil
	}
	if err := src.Assign(from); err != nil {
		return nil, 0, false, fmt.Errorf("assigning the partitions of the dead-letter topic: %w", err)
	}
	// Without this the consumer would go on fetching what nobody reads. A
	// release that fails changes nothing: the next scan assigns afresh.
	defer src.Unassign()

	var (
		best    []entry
		scanned int
	)
	for len(reading) > 0 {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				// Nothing read and an error the consumer was waiting out (the account
				// may describe the topic but not read it, say) is a failure, not a
				// topic with nothing in it.
				if lerr := src.LastError(); scanned == 0 && lerr != nil {
					return nil, 0, false, fmt.Errorf("reading the dead-letter topic: %w", lerr)
				}
				return lettersOf(best), scanned, true, nil
			}
			return nil, 0, false, err
		}
		ev, ok, err := src.Next(pollFor(ctx))
		if err != nil {
			return nil, 0, false, fmt.Errorf("reading the dead-letter topic: %w", err)
		}
		if !ok {
			continue
		}
		sp, wanted := reading[ev.partition]
		switch {
		case !wanted: // finished already, or never asked for
		case ev.eof:
			// The end of the partition ends the range only once the range is read to
			// within its last offset, which a marker can take. Earlier it is left
			// over from an earlier assignment.
			if sp.next >= sp.end-1 {
				delete(reading, ev.partition)
			}
		case ev.offset < sp.next: // before the range, or delivered twice
		case ev.offset >= sp.end: // produced after the scan began: the range is done
			if sp.started {
				delete(reading, ev.partition)
			}
		default:
			if !sp.started && ev.offset != sp.next {
				if !sp.lowKnown {
					low, err := lowOf(ctx, src, ev.partition)
					if err != nil {
						return nil, 0, false, fmt.Errorf("reading the partitions of the dead-letter topic: %w", err)
					}
					sp.low, sp.lowKnown = low, true
				}
				if ev.offset != sp.low {
					continue // left over from an earlier assignment
				}
			}
			sp.started = true
			sp.next = ev.offset + 1
			scanned++
			if ev.key == feedID {
				best = keep(best, ev, limit)
			}
			if sp.next >= sp.end {
				delete(reading, ev.partition)
			}
		}
	}
	return lettersOf(best), scanned, false, nil
}

// lowOf asks again for the first offset a partition holds.
func lowOf(ctx context.Context, src source, partition int32) (int64, error) {
	parts, err := src.Bounds(ctx)
	if err != nil {
		return 0, err
	}
	for _, b := range parts {
		if b.partition == partition {
			return b.low, nil
		}
	}
	return -1, nil
}

// pollFor is how long to wait for the next message: a poll interval, or what
// is left of the time if that is less.
func pollFor(ctx context.Context) time.Duration {
	wait := pollWait
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	return max(wait, time.Millisecond)
}

// eventTime is when the dead letter arrived: the received-at header the
// publisher wrote, else the time Kafka recorded the message. It is the time
// a Letter shows and the one letters are ordered by.
func eventTime(ev event) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, headerText(ev.headers, "received-at")); err == nil {
		return at.UTC()
	}
	return ev.at.UTC()
}

// stamp is where a message stands in time: when it arrived (eventTime), then
// where it sits in its partition.
type stamp struct {
	at        time.Time
	partition int32
	offset    int64
}

// newer reports whether s is more recent than o: it arrived later or, at
// the same time, sits at a higher partition and then a higher offset. The
// partition and the offset only make the order the same on every read; within a
// partition a higher offset is the later message.
func (s stamp) newer(o stamp) bool {
	switch {
	case !s.at.Equal(o.at):
		return s.at.After(o.at)
	case s.partition != o.partition:
		return s.partition > o.partition
	default:
		return s.offset > o.offset
	}
}

// entry is a letter that is among the newest so far.
type entry struct {
	stamp  stamp
	letter Letter
}

// keep adds the message to best, which holds the newest messages so far,
// newest first, and has room for limit of them. A message that would not be
// among them is not turned into a letter, so what a read holds is bounded by
// limit however many messages it covers.
func keep(best []entry, ev event, limit int) []entry {
	st := stamp{at: eventTime(ev), partition: ev.partition, offset: ev.offset}
	if len(best) == limit && !st.newer(best[limit-1].stamp) {
		return best
	}
	i := sort.Search(len(best), func(i int) bool { return st.newer(best[i].stamp) })
	best = append(best, entry{})
	copy(best[i+1:], best[i:])
	best[i] = entry{stamp: st, letter: letterOf(ev)}
	if len(best) > limit {
		best = best[:limit]
	}
	return best
}

func lettersOf(best []entry) []Letter {
	out := make([]Letter, len(best))
	for i, e := range best {
		out[i] = e.letter
	}
	return out
}

// letterOf reads a message of the dead-letter topic as the publisher wrote it
// (publish.Publisher.DeadLetter): the payload as the value, the rest in headers.
func letterOf(ev event) Letter {
	payload, truncated := cutPayload(ev.value)
	l := Letter{
		At:      eventTime(ev),
		Reason:  headerText(ev.headers, "reason"),
		Error:   headerText(ev.headers, commonkafka.HeaderError),
		Driver:  headerText(ev.headers, "driver"),
		Decoder: headerText(ev.headers, "decoder"),
		Sender:  headerText(ev.headers, "sender"),
		Size:    len(ev.value), Payload: payload, PayloadTruncated: truncated,
		Partition: ev.partition, Offset: ev.offset,
	}
	return l
}

// headerText is the value of the first header with the key, or "".
func headerText(headers []header, key string) string {
	for _, h := range headers {
		if h.key == key {
			return string(h.value)
		}
	}
	return ""
}

// cutPayload copies the first PayloadMax bytes of v and reports whether that
// left something out. A cut that falls inside a multi-byte character leaves
// the character out whole: a text payload would otherwise turn into one that
// is not valid UTF-8, and be taken for binary. Bytes that are not UTF-8 are cut
// where they are.
func cutPayload(v []byte) (cut []byte, truncated bool) {
	end := len(v)
	if end > PayloadMax {
		end = PayloadMax
		if !utf8.RuneStart(v[end]) {
			// v[end] continues a character that began before it, at most
			// UTFMax-1 bytes back.
			for k := end - 1; k >= 0 && k >= end-(utf8.UTFMax-1); k-- {
				if utf8.RuneStart(v[k]) {
					end = k
					break
				}
			}
		}
	}
	return append([]byte(nil), v[:end]...), end < len(v)
}
