package dlq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// fakeMsg is one message of the fake dead-letter topic, in the shape the
// publisher writes it: the feed id as the key, the payload as the value, the
// rest in headers.
type fakeMsg struct {
	key     string
	at      time.Time
	headers map[string]string
	value   string
}

// msg is a dead letter of a feed with the headers the publisher sets.
func msg(key string, when time.Time, value string) fakeMsg {
	return fakeMsg{key: key, at: when, value: value, headers: map[string]string{
		"reason": "parse_error", "error": "bad json", "driver": "tcp", "decoder": "sentinel", "sender": "10.0.0.5:51234",
	}}
}

// fakePartition holds msgs[i] at offset low+i, as a log whose head was trimmed
// by retention would.
type fakePartition struct {
	low  int64
	msgs []fakeMsg
	// late are produced after the scan asked where the partition ends: they
	// follow msgs in the log, past the end the scan was told.
	late []fakeMsg
}

func (p fakePartition) high() int64 { return p.low + int64(len(p.msgs)) }

// partitionOf is a partition that starts at offset 0.
func partitionOf(msgs ...fakeMsg) fakePartition { return fakePartition{msgs: msgs} }

// fakeSource is a consumer over a fake topic. Assign queues what librdkafka
// would deliver from each starting offset, one partition after the other in
// turn, and Next hands it out; when nothing is queued Next waits, as a poll does.
type fakeSource struct {
	parts map[int32]fakePartition

	// eof reports the end of each partition once it has been read, as
	// enable.partition.eof makes librdkafka do.
	eof bool
	// stall maps a partition to the offset from which nothing more arrives, and
	// no end is reported: a broker that stopped answering.
	stall map[int32]int64
	// strays are delivered before anything else, whatever was assigned.
	strays []event
	// lose maps a partition to offsets that are never delivered (a transaction marker, say).
	lose map[int32]map[int64]bool

	boundsErr, assignErr, nextErr error
	// lastErr is what LastError reports: an error the consumer waits out.
	lastErr error
	// lowNow maps a partition to the first offset it holds after retention
	// removed the start of what the scan asked for: the second look at the
	// bounds shows it, and the consumer resets to it.
	lowNow map[int32]int64

	// what the scan did
	assigned                               map[int32]int64
	boundsCalls, assigns, unassigns, nexts int
	closed                                 int
	queue                                  []event
}

func (s *fakeSource) Bounds(context.Context) ([]bounds, error) {
	s.boundsCalls++
	if s.boundsErr != nil {
		return nil, s.boundsErr
	}
	var out []bounds
	for id, p := range s.parts {
		low := p.low
		if now, ok := s.lowNow[id]; ok && s.boundsCalls > 1 {
			low = now
		}
		out = append(out, bounds{partition: id, low: low, high: p.high()})
	}
	return out, nil
}

func (s *fakeSource) Assign(from map[int32]int64) error {
	s.assigns++
	if s.assignErr != nil {
		return s.assignErr
	}
	s.assigned = from
	s.queue = append([]event(nil), s.strays...)
	ids := make([]int32, 0, len(from))
	for id := range from {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	lanes := map[int32][]event{}
	for _, id := range ids {
		p := s.parts[id]
		end := p.high()
		stalled := false
		if off, ok := s.stall[id]; ok && off < end {
			end, stalled = off, true
		}
		for off := max(from[id], s.lowNow[id]); off < end; off++ {
			if s.lose[id][off] {
				continue
			}
			m := p.msgs[off-p.low]
			lanes[id] = append(lanes[id], toEvent(id, off, m))
		}
		if !stalled {
			for i, m := range p.late {
				lanes[id] = append(lanes[id], toEvent(id, p.high()+int64(i), m))
			}
		}
		if s.eof && !stalled {
			lanes[id] = append(lanes[id], event{partition: id, eof: true})
		}
	}
	for more := true; more; {
		more = false
		for _, id := range ids {
			if len(lanes[id]) > 0 {
				s.queue = append(s.queue, lanes[id][0])
				lanes[id] = lanes[id][1:]
				more = true
			}
		}
	}
	return nil
}

func toEvent(partition int32, offset int64, m fakeMsg) event {
	ev := event{partition: partition, offset: offset, at: m.at, key: m.key, value: []byte(m.value)}
	keys := make([]string, 0, len(m.headers))
	for k := range m.headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ev.headers = append(ev.headers, header{key: k, value: []byte(m.headers[k])})
	}
	return ev
}

func (s *fakeSource) Unassign() error { s.unassigns++; return nil }

func (s *fakeSource) Next(wait time.Duration) (event, bool, error) {
	s.nexts++
	if s.nextErr != nil {
		return event{}, false, s.nextErr
	}
	if len(s.queue) == 0 {
		time.Sleep(wait)
		return event{}, false, nil
	}
	ev := s.queue[0]
	s.queue = s.queue[1:]
	return ev, true, nil
}

func (s *fakeSource) LastError() error { return s.lastErr }

func (s *fakeSource) Close() error { s.closed++; return nil }

// refs names letters by where they sit, in the order given.
func refs(letters []Letter) []string {
	out := make([]string, len(letters))
	for i, l := range letters {
		out[i] = fmt.Sprintf("p%d@%d", l.Partition, l.Offset)
	}
	return out
}

func equal(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// within runs scan under a deadline, the way KafkaReader does.
func within(d time.Duration, src source, maxScan int64, feed string, limit int) ([]Letter, int, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return scan(ctx, src, maxScan, feed, limit)
}

func TestRecentReturnsTheNewestAcrossPartitions(t *testing.T) {
	src := &fakeSource{parts: map[int32]fakePartition{
		0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(5), "b"), msg("feed-a", at(9), "c")),
		1: partitionOf(msg("feed-a", at(2), "d"), msg("feed-a", at(6), "e"), msg("feed-a", at(8), "f")),
		2: partitionOf(msg("feed-a", at(3), "g"), msg("feed-a", at(4), "h"), msg("feed-a", at(7), "i")),
	}, eof: true}
	letters, scanned, partial, err := within(5*time.Second, src, 2000, "feed-a", 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p0@2", "p1@2", "p2@2", "p1@1", "p0@1"}; !equal(refs(letters), want) {
		t.Errorf("letters = %v, want %v (newest first across partitions)", refs(letters), want)
	}
	if scanned != 9 || partial {
		t.Errorf("scanned %d partial %v, want 9 false", scanned, partial)
	}
	if letters[0].Payload == nil || string(letters[0].Payload) != "c" || !letters[0].At.Equal(at(9)) {
		t.Errorf("newest = %+v", letters[0])
	}
	if src.unassigns != 1 {
		t.Errorf("the consumer was released %d times, want once", src.unassigns)
	}
}

// Messages recorded in the same millisecond are ordered by partition and then
// offset, higher first, so that the order is the same on every request and,
// within a partition, newest first.
func TestRecentBreaksTiesByPartitionAndOffset(t *testing.T) {
	same := at(1)
	src := &fakeSource{parts: map[int32]fakePartition{
		0: partitionOf(msg("feed-a", same, "a"), msg("feed-a", same, "b")),
		1: partitionOf(msg("feed-a", same, "c"), msg("feed-a", same, "d")),
	}, eof: true}
	letters, _, _, err := within(5*time.Second, src, 2000, "feed-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p1@1", "p1@0", "p0@1", "p0@0"}; !equal(refs(letters), want) {
		t.Errorf("letters = %v, want %v", refs(letters), want)
	}
}

// Only the last maxScan messages of each partition are read, from the first
// offset the partition still holds if that is later.
func TestRecentReadsOnlyTheLastMaxScanPerPartition(t *testing.T) {
	thirty := func(base int) []fakeMsg {
		out := make([]fakeMsg, 30)
		for i := range out {
			out[i] = msg("feed-a", at(base+i), "x")
		}
		return out
	}
	src := &fakeSource{parts: map[int32]fakePartition{
		0: {low: 0, msgs: thirty(0)},    // offsets 0..29: read from 20
		1: {low: 20, msgs: thirty(100)}, // offsets 20..49: read from 40
		2: {low: 45, msgs: thirty(200)[:5]},
		3: {low: 7, msgs: nil}, // empty: nothing to read, and nothing assigned
	}, eof: true}
	letters, scanned, partial, err := within(5*time.Second, src, 10, "feed-a", 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int32]int64{0: 20, 1: 40, 2: 45}; fmt.Sprint(src.assigned) != fmt.Sprint(want) {
		t.Errorf("assigned %v, want %v: each partition from max(low, high-maxScan), and none that is empty", src.assigned, want)
	}
	if scanned != 25 || partial || len(letters) != 25 {
		t.Errorf("scanned %d, %d letters, partial %v; want 25, 25, false", scanned, len(letters), partial)
	}
	for _, l := range letters {
		if from := src.assigned[l.Partition]; l.Offset < from {
			t.Errorf("%v is before the range read of its partition (from %d)", refs([]Letter{l}), from)
		}
	}
}

func TestRecentWithNothingToReadAssignsNothing(t *testing.T) {
	src := &fakeSource{parts: map[int32]fakePartition{0: {low: 3}, 1: {}}}
	letters, scanned, partial, err := within(time.Second, src, 2000, "feed-a", 20)
	if err != nil || len(letters) != 0 || scanned != 0 || partial {
		t.Fatalf("got %v, %d, %v, %v", letters, scanned, partial, err)
	}
	if src.assigns != 0 {
		t.Errorf("assigned %d times with no partition to read", src.assigns)
	}
}

// The key is the feed id: other feeds' letters are read, so they count as
// scanned, and left out.
func TestRecentKeepsOnlyTheFeedsLetters(t *testing.T) {
	src := &fakeSource{parts: map[int32]fakePartition{
		0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-b", at(2), "b"), msg("feed-a", at(3), "c"), msg("", at(4), "d")),
		1: partitionOf(msg("feed-b", at(5), "e"), msg("feed-a-2", at(6), "f"), msg("feed-a", at(7), "g")),
	}, eof: true}
	letters, scanned, _, err := within(5*time.Second, src, 2000, "feed-a", 20)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p1@2", "p0@2", "p0@0"}; !equal(refs(letters), want) {
		t.Errorf("letters = %v, want %v", refs(letters), want)
	}
	if scanned != 7 {
		t.Errorf("scanned %d, want 7: every message read counts, not only the feed's", scanned)
	}
}

func TestRecentKeepsAtMostLimit(t *testing.T) {
	var msgs []fakeMsg
	for i := 0; i < 50; i++ {
		msgs = append(msgs, msg("feed-a", at(i), "x"))
	}
	src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(msgs...)}, eof: true}
	letters, scanned, _, err := within(5*time.Second, src, 2000, "feed-a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p0@49", "p0@48", "p0@47"}; !equal(refs(letters), want) || scanned != 50 {
		t.Errorf("letters = %v, scanned %d", refs(letters), scanned)
	}
	// No limit, no read.
	src = &fakeSource{parts: map[int32]fakePartition{0: partitionOf(msgs...)}, eof: true}
	if letters, _, _, err := within(time.Second, src, 2000, "feed-a", 0); err != nil || len(letters) != 0 || src.boundsCalls != 0 {
		t.Errorf("limit 0: %v, %v, %d bound queries", letters, err, src.boundsCalls)
	}
}

// A scan is over when every partition has been read up to where it ended when
// the scan began, whether or not the end is reported.
func TestRecentIsCompleteOnceEveryPartitionIsRead(t *testing.T) {
	for _, eof := range []bool{false, true} {
		src := &fakeSource{parts: map[int32]fakePartition{
			0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b")),
			1: partitionOf(msg("feed-a", at(3), "c")),
		}, eof: eof}
		start := time.Now()
		letters, _, partial, err := within(5*time.Second, src, 2000, "feed-a", 20)
		if err != nil || partial || len(letters) != 3 {
			t.Errorf("eof %v: %d letters, partial %v, %v", eof, len(letters), partial, err)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("eof %v: a complete scan took %v", eof, took)
		}
	}
}

// librdkafka does not deliver every offset (a transaction marker has none to
// give), so the end of a partition reported by the consumer also completes it.
func TestRecentEndOfPartitionCompletesIt(t *testing.T) {
	src := &fakeSource{
		parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"), msg("feed-a", at(3), "c"))},
		lose:  map[int32]map[int64]bool{0: {2: true}}, // the last offset never arrives
		eof:   true,
	}
	start := time.Now()
	letters, scanned, partial, err := within(5*time.Second, src, 2000, "feed-a", 20)
	if err != nil || partial || len(letters) != 2 || scanned != 2 {
		t.Fatalf("%d letters, scanned %d, partial %v, %v", len(letters), scanned, partial, err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v: it waited for an offset that does not come", took)
	}
}

// At the deadline the scan returns what it has read and says it is partial.
func TestRecentStopsAtTheDeadline(t *testing.T) {
	src := &fakeSource{
		parts: map[int32]fakePartition{
			0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"), msg("feed-a", at(3), "c"), msg("feed-a", at(4), "d"), msg("feed-a", at(5), "e")),
			1: partitionOf(msg("feed-a", at(6), "f")),
		},
		stall: map[int32]int64{0: 3}, // partition 0 delivers offsets 0..2 and then goes quiet
		eof:   true,
	}
	start := time.Now()
	letters, scanned, partial, err := within(250*time.Millisecond, src, 2000, "feed-a", 20)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Error("partial = false after the deadline")
	}
	if want := []string{"p1@0", "p0@2", "p0@1", "p0@0"}; !equal(refs(letters), want) || scanned != 4 {
		t.Errorf("letters = %v, scanned %d, want %v and 4", refs(letters), scanned, want)
	}
	if took < 200*time.Millisecond || took > 3*time.Second {
		t.Errorf("returned after %v, want about the 250ms deadline", took)
	}
	if src.unassigns != 1 {
		t.Errorf("released %d times, want once", src.unassigns)
	}
}

// A caller that goes away is not a partial read: nobody is left to receive it.
func TestRecentStopsWhenTheCallerGoesAway(t *testing.T) {
	src := &fakeSource{
		parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"))},
		stall: map[int32]int64{0: 1},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	letters, _, partial, err := scan(ctx, src, 2000, "feed-a", 20)
	if !errors.Is(err, context.Canceled) || partial || letters != nil {
		t.Errorf("got %v, partial %v, %v; want context.Canceled", letters, partial, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v to notice", took)
	}
	if src.unassigns != 1 {
		t.Errorf("released %d times, want once", src.unassigns)
	}
}

// librdkafka can deliver what the scan did not ask for. Whatever lies outside
// the range read, or repeats a message, is not part of the answer.
func TestRecentIgnoresWhatIsNotInTheRangeRead(t *testing.T) {
	var msgs []fakeMsg
	for i := 0; i < 6; i++ {
		msgs = append(msgs, msg("feed-a", at(i), "x"))
	}
	// maxScan 4 asks for offsets 2..5 of partition 0.
	want := []string{"p0@5", "p0@4", "p0@3", "p0@2"}

	t.Run("outside the range or the partitions read", func(t *testing.T) {
		src := &fakeSource{
			parts: map[int32]fakePartition{0: partitionOf(msgs...)},
			eof:   true,
			strays: []event{
				toEvent(0, 1, msg("feed-a", at(100), "before the range")),
				toEvent(7, 0, msg("feed-a", at(101), "unknown partition")),
				{partition: 9, eof: true}, // the end of a partition that is not being read
			},
		}
		letters, scanned, partial, err := within(5*time.Second, src, 4, "feed-a", 20)
		if err != nil || partial || !equal(refs(letters), want) || scanned != 4 {
			t.Errorf("letters = %v, scanned %d, partial %v, %v; want %v and 4", refs(letters), scanned, partial, err, want)
		}
	})

	t.Run("a message delivered twice", func(t *testing.T) {
		src := &fakeSource{
			parts:  map[int32]fakePartition{0: partitionOf(msgs...)},
			eof:    true,
			strays: []event{toEvent(0, 2, msg("feed-a", at(2), "x")), toEvent(0, 2, msg("feed-a", at(2), "x"))},
		}
		letters, scanned, _, err := within(5*time.Second, src, 4, "feed-a", 20)
		if err != nil || !equal(refs(letters), want) || scanned != 4 {
			t.Errorf("letters = %v, scanned %d, %v; want %v and 4", refs(letters), scanned, err, want)
		}
	})

	// The partition ended at offset 2 when the scan asked, and what came after
	// is not part of it. Here the last offset asked for never arrives, so the
	// first message past it is what shows the partition is done.
	t.Run("a message produced after the scan began", func(t *testing.T) {
		src := &fakeSource{
			parts: map[int32]fakePartition{0: {
				msgs: []fakeMsg{msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b")},
				late: []fakeMsg{msg("feed-a", at(3), "later")},
			}},
			lose: map[int32]map[int64]bool{0: {1: true}},
		}
		letters, scanned, partial, err := within(5*time.Second, src, 2000, "feed-a", 20)
		if err != nil || partial || !equal(refs(letters), []string{"p0@0"}) || scanned != 1 {
			t.Errorf("letters = %v, scanned %d, partial %v, %v; want only p0@0", refs(letters), scanned, partial, err)
		}
	})
}

func TestRecentCutsThePayloadAtAKilobyte(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantLen  int
		wantCut  bool
		wantUTF8 bool
	}{
		{"short", "not json", 8, false, true},
		{"exactly a kilobyte", strings.Repeat("a", 1024), 1024, false, true},
		{"a byte over", strings.Repeat("a", 1025), 1024, true, true},
		{"much longer", strings.Repeat("a", 100000), 1024, true, true},
		// A cut inside a character would make a text payload look binary, so the
		// character is left out whole.
		{"two-byte character split", strings.Repeat("a", 1023) + "é" + "tail", 1023, true, true},
		{"three-byte character split", strings.Repeat("a", 1022) + "€" + "tail", 1022, true, true},
		{"four-byte character split", strings.Repeat("a", 1021) + "😀" + "tail", 1021, true, true},
		{"character that ends at the cut", strings.Repeat("a", 1022) + "é" + "tail", 1024, true, true},
		// Binary stays as it is, cut at a kilobyte.
		{"binary", strings.Repeat("\xff", 3000), 1024, true, false},
		{"continuation bytes only", strings.Repeat("\x80", 3000), 1024, true, false},
		{"empty", "", 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), tc.payload))}, eof: true}
			letters, _, _, err := within(5*time.Second, src, 2000, "feed-a", 20)
			if err != nil || len(letters) != 1 {
				t.Fatalf("%d letters, %v", len(letters), err)
			}
			l := letters[0]
			if len(l.Payload) != tc.wantLen || l.PayloadTruncated != tc.wantCut || utf8.Valid(l.Payload) != tc.wantUTF8 {
				t.Errorf("payload %d bytes, truncated %v, utf8 %v; want %d, %v, %v", len(l.Payload), l.PayloadTruncated, utf8.Valid(l.Payload), tc.wantLen, tc.wantCut, tc.wantUTF8)
			}
			if l.Size != len(tc.payload) {
				t.Errorf("size = %d, want the whole payload's %d", l.Size, len(tc.payload))
			}
			if !strings.HasPrefix(tc.payload, string(l.Payload)) {
				t.Error("the cut payload is not the start of the payload")
			}
		})
	}
}

// A payload the publisher cut at its cap (64 KiB) still reports the size it
// arrived with, and is flagged as cut, even when what the topic holds is short
// enough to show whole.
func TestRecentReportsTheOriginalSizeOfACutPayload(t *testing.T) {
	big := msg("feed-a", at(1), strings.Repeat("a", 65536))
	big.headers["raw-truncated"], big.headers["raw-size"] = "true", "1048576"
	small := msg("feed-a", at(2), "tiny") // a raw-size that is not a number
	small.headers["raw-size"] = "garbage"
	smaller := msg("feed-a", at(3), "tiny")
	smaller.headers["raw-size"] = "2"
	src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(big, small, smaller)}, eof: true}
	letters, _, _, err := within(5*time.Second, src, 2000, "feed-a", 20)
	if err != nil || len(letters) != 3 {
		t.Fatalf("%d letters, %v", len(letters), err)
	}
	byOffset := map[int64]Letter{}
	for _, l := range letters {
		byOffset[l.Offset] = l
	}
	if l := byOffset[0]; l.Size != 1048576 || len(l.Payload) != 1024 || !l.PayloadTruncated {
		t.Errorf("cut payload: size %d, shown %d, truncated %v; want 1048576, 1024, true", l.Size, len(l.Payload), l.PayloadTruncated)
	}
	for _, off := range []int64{1, 2} {
		if l := byOffset[off]; l.Size != 4 || l.PayloadTruncated {
			t.Errorf("offset %d: size %d truncated %v, want the value's own 4 and not truncated (an unusable raw-size is ignored)", off, l.Size, l.PayloadTruncated)
		}
	}
}

// What the publisher writes to the headers is what the letter says.
func TestRecentReadsTheLetterFromItsHeaders(t *testing.T) {
	full := msg("feed-a", at(10), "payload")
	full.headers = map[string]string{
		"producer": "ingest-service", "feed-id": "feed-a",
		"reason": "missing_identity", "error": "source_entity_id or an identity is required",
		"driver": "udp", "decoder": "nmea-ais", "sender": "10.0.0.9:4001",
		"received-at": "2026-10-18T11:59:58.250Z",
	}
	bare := fakeMsg{key: "feed-a", at: at(20), value: "old"} // from before the headers were added
	badTime := msg("feed-a", at(30), "x")
	badTime.headers["received-at"] = "yesterday"
	local := msg("feed-a", at(40), "x")
	local.headers["received-at"] = "2026-10-18T14:00:00.5+02:00"

	src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(full, bare, badTime, local)}, eof: true}
	letters, _, _, err := within(5*time.Second, src, 2000, "feed-a", 20)
	if err != nil || len(letters) != 4 {
		t.Fatalf("%d letters, %v", len(letters), err)
	}
	byOffset := map[int64]Letter{}
	for _, l := range letters {
		byOffset[l.Offset] = l
	}

	got := byOffset[0]
	want := Letter{
		At: time.Date(2026, 10, 18, 11, 59, 58, 250000000, time.UTC), Reason: "missing_identity",
		Error: "source_entity_id or an identity is required", Driver: "udp", Decoder: "nmea-ais", Sender: "10.0.0.9:4001",
		Size: 7, Payload: []byte("payload"), Partition: 0, Offset: 0,
	}
	if !got.At.Equal(want.At) || got.At.Location() != time.UTC {
		t.Errorf("at = %v, want %v in UTC: the time the frame arrived", got.At, want.At)
	}
	got.At, want.At = time.Time{}, time.Time{}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("letter\n got %+v\nwant %+v", got, want)
	}

	if l := byOffset[1]; l.Reason != "" || l.Error != "" || l.Driver != "" || l.Decoder != "" || l.Sender != "" || !l.At.Equal(at(20)) {
		t.Errorf("a letter without headers = %+v, want empty texts and the message's time", l)
	}
	if l := byOffset[2]; !l.At.Equal(at(30)) {
		t.Errorf("an unreadable received-at: at = %v, want the message's time %v", l.At, at(30))
	}
	if l := byOffset[3]; !l.At.Equal(time.Date(2026, 10, 18, 12, 0, 0, 500000000, time.UTC)) || l.At.Location() != time.UTC {
		t.Errorf("received-at with an offset: at = %v, want 12:00:00.5 UTC", l.At)
	}
}

func TestRecentReportsFailuresOfTheTopic(t *testing.T) {
	boom := errors.New("broker transport failure")
	parts := map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"))}
	t.Run("partitions", func(t *testing.T) {
		src := &fakeSource{parts: parts, boundsErr: boom}
		if _, _, _, err := within(time.Second, src, 2000, "feed-a", 20); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
		if src.assigns != 0 {
			t.Error("assigned after the partitions could not be read")
		}
	})
	t.Run("assign", func(t *testing.T) {
		src := &fakeSource{parts: parts, assignErr: boom}
		if _, _, _, err := within(time.Second, src, 2000, "feed-a", 20); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("read", func(t *testing.T) {
		src := &fakeSource{parts: parts, nextErr: boom}
		letters, _, partial, err := within(time.Second, src, 2000, "feed-a", 20)
		if !errors.Is(err, boom) || letters != nil || partial {
			t.Errorf("got %v, partial %v, %v", letters, partial, err)
		}
		if src.unassigns != 1 {
			t.Errorf("released %d times after a failed read, want once", src.unassigns)
		}
	})
}

// An error the consumer only reports while it fetches, such as the account being
// allowed to describe the topic but not to read it, shows as nothing arriving.
// When the deadline comes with nothing read, that error is the answer, not an
// empty list marked partial.
func TestRecentReturnsTheErrorTheConsumerWaitedOut(t *testing.T) {
	denied := errors.New("Broker: Topic authorization failed")
	src := &fakeSource{
		parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"))},
		stall: map[int32]int64{0: 0}, lastErr: denied,
	}
	letters, scanned, partial, err := within(200*time.Millisecond, src, 2000, "feed-a", 20)
	if !errors.Is(err, denied) || letters != nil || scanned != 0 || partial {
		t.Errorf("got %v, %d, %v, %v; want the error", letters, scanned, partial, err)
	}
	if src.unassigns != 1 {
		t.Errorf("released %d times, want once", src.unassigns)
	}
}

// A transient error does not fail a read that is making progress: it succeeds
// when the messages come, and at the deadline with some read it is partial, not
// an error.
func TestRecentIgnoresATransientErrorWhileItReads(t *testing.T) {
	blip := errors.New("Local: Broker transport failure")
	whole := &fakeSource{
		parts:   map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"))},
		lastErr: blip, eof: true,
	}
	letters, scanned, partial, err := within(5*time.Second, whole, 2000, "feed-a", 20)
	if err != nil || partial || len(letters) != 2 || scanned != 2 {
		t.Errorf("a read with a transient error: %v, %d, %v, %v", refs(letters), scanned, partial, err)
	}

	cut := &fakeSource{
		parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"), msg("feed-a", at(2), "b"), msg("feed-a", at(3), "c"))},
		stall: map[int32]int64{0: 2}, lastErr: blip,
	}
	letters, scanned, partial, err = within(250*time.Millisecond, cut, 2000, "feed-a", 20)
	if err != nil || !partial || len(letters) != 2 || scanned != 2 {
		t.Errorf("a read cut short with a transient error: %v, %d, %v, %v; want partial, no error", refs(letters), scanned, partial, err)
	}
}

// An error that will not pass fails the read when it comes, not at the deadline.
func TestRecentFailsAtOnceOnAnErrorThatWillNotPass(t *testing.T) {
	denied := errors.New("Broker: Topic authorization failed")
	src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"))}, nextErr: denied}
	start := time.Now()
	_, _, partial, err := within(5*time.Second, src, 2000, "feed-a", 20)
	if !errors.Is(err, denied) || partial {
		t.Errorf("err = %v, partial %v", err, partial)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v: it waited for the deadline", took)
	}
}

// What an earlier assignment left in the consumer can arrive first. Until the
// range's first message has come, only the offset asked for is taken, and an
// end of the partition counts only once the range is read to within its last
// offset.
func TestRecentIgnoresWhatAnEarlierAssignmentLeft(t *testing.T) {
	var msgs []fakeMsg
	for i := 0; i < 6; i++ {
		msgs = append(msgs, msg("feed-a", at(i), "x"))
	}
	want := []string{"p0@5", "p0@4", "p0@3", "p0@2"} // maxScan 4 asks for offsets 2..5

	t.Run("a message inside the range but not its first", func(t *testing.T) {
		src := &fakeSource{
			parts:  map[int32]fakePartition{0: partitionOf(msgs...)},
			eof:    true,
			strays: []event{toEvent(0, 4, msg("feed-a", at(100), "stale")), toEvent(0, 5, msg("feed-a", at(101), "stale"))},
		}
		letters, scanned, partial, err := within(5*time.Second, src, 4, "feed-a", 20)
		if err != nil || partial || !equal(refs(letters), want) || scanned != 4 {
			t.Errorf("letters = %v, scanned %d, partial %v, %v; want %v and 4", refs(letters), scanned, partial, err, want)
		}
		for _, l := range letters {
			if string(l.Payload) == "stale" {
				t.Errorf("a stale message was taken: %+v", l)
			}
		}
	})

	t.Run("an end of the partition before the range is read", func(t *testing.T) {
		src := &fakeSource{
			parts:  map[int32]fakePartition{0: partitionOf(msgs...)},
			eof:    true,
			strays: []event{{partition: 0, eof: true}},
		}
		letters, scanned, partial, err := within(5*time.Second, src, 4, "feed-a", 20)
		if err != nil || partial || !equal(refs(letters), want) || scanned != 4 {
			t.Errorf("letters = %v, scanned %d, partial %v, %v; want %v and 4", refs(letters), scanned, partial, err, want)
		}
	})

	// Retention removed offsets 2 and 3 after the scan asked: the consumer
	// resets to what is left, so the first message is the new low, not 2.
	t.Run("retention moved the start", func(t *testing.T) {
		src := &fakeSource{
			parts:  map[int32]fakePartition{0: partitionOf(msgs...)},
			eof:    true,
			lowNow: map[int32]int64{0: 4},
			strays: []event{toEvent(0, 3, msg("feed-a", at(100), "stale"))},
		}
		letters, scanned, partial, err := within(5*time.Second, src, 4, "feed-a", 20)
		if want := []string{"p0@5", "p0@4"}; err != nil || partial || !equal(refs(letters), want) || scanned != 2 {
			t.Errorf("letters = %v, scanned %d, partial %v, %v; want %v and 2", refs(letters), scanned, partial, err, want)
		}
	})
}

// Letters are ordered by the time they show: when the frame arrived, and when
// Kafka recorded the message if the header says nothing.
func TestRecentOrdersByTheTimeItShows(t *testing.T) {
	first := msg("feed-a", at(5), "a") // arrived at 9, recorded at 5
	first.headers["received-at"] = at(9).Format(time.RFC3339Nano)
	second := msg("feed-a", at(6), "b") // arrived at 2, recorded at 6: newest by Kafka's clock only
	second.headers["received-at"] = at(2).Format(time.RFC3339Nano)
	third := msg("feed-a", at(7), "c") // no header: recorded at 7
	delete(third.headers, "received-at")
	src := &fakeSource{parts: map[int32]fakePartition{0: partitionOf(first, second, third)}, eof: true}
	letters, _, _, err := within(5*time.Second, src, 2000, "feed-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p0@0", "p0@2"}; !equal(refs(letters), want) {
		t.Fatalf("letters = %v, want %v: newest by what they show", refs(letters), want)
	}
	for i := 1; i < len(letters); i++ {
		if letters[i].At.After(letters[i-1].At) {
			t.Errorf("letter %d (%v) is newer than the one before it (%v)", i, letters[i].At, letters[i-1].At)
		}
	}
}
