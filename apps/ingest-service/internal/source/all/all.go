// Package all registers every built-in transport, decoder and driver.
// Import it for its side effects (the server does; tests that need the
// catalog do too). Adding a driver means adding one line here.
package all

import (
	// Transports.
	_ "github.com/sentinel/ingest-service/internal/source/transport/mqtt"
	_ "github.com/sentinel/ingest-service/internal/source/transport/stomp"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpclient"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpserver"
	_ "github.com/sentinel/ingest-service/internal/source/transport/udp"
	_ "github.com/sentinel/ingest-service/internal/source/transport/websocket"

	// Decoders.
	_ "github.com/sentinel/ingest-service/internal/source/decoder/beast"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/cot"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/jsonmapped"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/link16"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/nmeaais"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/nmeagps"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sbs"

	// Drivers.
	_ "github.com/sentinel/ingest-service/internal/source/driver/aisstream"
	_ "github.com/sentinel/ingest-service/internal/source/driver/celestrak"
	_ "github.com/sentinel/ingest-service/internal/source/driver/httpjson"
	_ "github.com/sentinel/ingest-service/internal/source/driver/intelsky"
	_ "github.com/sentinel/ingest-service/internal/source/driver/opensky"
	_ "github.com/sentinel/ingest-service/internal/source/driver/stream"
	_ "github.com/sentinel/ingest-service/internal/source/driver/tak"
)
