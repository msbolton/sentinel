package obs

import "time"

// DeadLetter is a payload nothing could decode, with where it came from. The
// publisher turns it into a message on the dead-letter topic; it lives here
// because both the feed runner and the publisher already import obs, and the
// runner must not import the publisher (it links the Kafka client).
type DeadLetter struct {
	FeedID string
	Driver string
	// Decoder names the decoder that refused the payload; empty when the
	// source has none (a polled response) or the driver did not say.
	Decoder string
	// Sender is the remote address, topic or queue the payload came from;
	// empty when the driver did not say.
	Sender string
	Reason string
	Cause  error
	Raw    []byte
	// ReceivedAt is when the frame arrived; zero when the payload was never
	// a frame, in which case the publisher stamps the time it dead-letters.
	ReceivedAt time.Time
}
