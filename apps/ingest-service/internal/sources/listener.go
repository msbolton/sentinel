package sources

// Listener is the common interface for all ingest data-feed listeners.
// All existing types (MQTTListener, STOMPListener, TCPListener, OpenSkyListener)
// satisfy this implicitly.
type Listener interface {
	Start() error
	Stop()
}
