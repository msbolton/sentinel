package ingesttoken

// FeedsChannel exposes feedsChannel to the external tests, which can import
// the registry package (the internal ones cannot: registry imports driver,
// and driver imports this package).
const FeedsChannel = feedsChannel
