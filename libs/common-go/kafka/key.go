package kafka

// ObservationKey is the Kafka key for an observation: the canonical natural
// identity when the decoder found one ("ICAO24:A1B2C3"), otherwise the
// feed-local key ("feed:<feedId>:<sourceEntityId>"). Keying by natural
// identity puts every feed's reports about one object on one partition, so
// the entity service fuses them in order.
func ObservationKey(identityType, identityValue, feedID, sourceEntityID string) string {
	if identityType != "" && identityValue != "" {
		return identityType + ":" + identityValue
	}
	return "feed:" + feedID + ":" + sourceEntityID
}
