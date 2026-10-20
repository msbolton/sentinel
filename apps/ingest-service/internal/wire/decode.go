package wire

import (
	"math"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

// FromProto turns a received sentinel.v1.Observation into an observation,
// the inverse of ToProto. The fields the ingest service owns
// (observation_id, feed_id, driver, decoder, received_at, replica_id,
// fencing_token, raw, emplacement_id, exercise) are not copied: the decoder
// and the runner set them.
// Optional values are copied, never shared with the message, and as sent:
// NaN and ±Inf pass through, since checking values is the decoder's job.
func FromProto(m *sentinelv1.Observation) *obs.Observation {
	o := &obs.Observation{
		SourceEntityID:    m.GetSourceEntityId(),
		Source:            enumName(m.GetSource()),
		SensorID:          m.GetSensorId(),
		EntityType:        enumName(m.GetEntityType()),
		TrackEnvironment:  enumName(m.GetTrackEnvironment()),
		DisplayName:       m.GetDisplayName(),
		CountryOfOrigin:   m.GetCountryOfOrigin(),
		Affiliation:       enumName(m.GetAffiliation()),
		OperationalStatus: enumName(m.GetOperationalStatus()),
		Classification:    enumName(m.GetClassification()),
		Heading:           copyFloat(m.Heading),
		SpeedKnots:        copyFloat(m.SpeedKnots),
		Course:            copyFloat(m.Course),
	}
	if ts := m.GetSourceTimestamp(); ts != nil {
		o.SourceTimestamp = ts.AsTime().UTC()
	}
	for _, id := range m.GetIdentities() {
		if t := enumName(id.GetType()); t != "" && id.GetValue() != "" {
			o.Identities = append(o.Identities, obs.Identity{Type: t, Value: id.GetValue()})
		}
	}
	if p := m.GetPosition(); p != nil {
		o.HasPosition, o.Latitude, o.Longitude = true, p.GetLatitude(), p.GetLongitude()
		o.Altitude = copyFloat(p.AltitudeMeters)
	}
	if v := m.GetVelocity(); v != nil {
		o.Velocity = &[3]float64{v.GetNorth(), v.GetEast(), v.GetUp()}
	}
	if a := m.GetAcceleration(); a != nil {
		o.Acceleration = &[3]float64{a.GetNorth(), a.GetEast(), a.GetUp()}
	}
	if r := m.GetOrientation(); r != nil {
		o.Orientation = &[3]float64{r.GetYaw(), r.GetPitch(), r.GetRoll()}
	}
	if c := m.GetPositionCovariance(); c != nil {
		o.PosCovariance = &[6]float64{c.GetPnPn(), c.GetPnPe(), c.GetPnPu(), c.GetPePe(), c.GetPePu(), c.GetPuPu()}
	}
	if u := m.GetUncertainty(); u != nil {
		if o.PosCovariance == nil && u.GetSemiMajor() > 0 && u.GetSemiMinor() > 0 &&
			!math.IsInf(u.GetSemiMajor(), 0) && !math.IsInf(u.GetSemiMinor(), 0) &&
			!math.IsNaN(u.GetOrientation()) && !math.IsInf(u.GetOrientation(), 0) {
			if cNN, cNE, cEE := geo.Cov2FromEllipse(u.GetSemiMajor(), u.GetSemiMinor(), u.GetOrientation()); !math.IsNaN(cNN) && !math.IsNaN(cNE) && !math.IsNaN(cEE) &&
				!math.IsInf(cNN, 0) && !math.IsInf(cNE, 0) && !math.IsInf(cEE, 0) {
				o.PosCovariance = &[6]float64{cNN, cNE, 0, cEE, 0, 0}
			}
		}
		ce := u.GetCircularError()
		if ce == 0 && o.PosCovariance != nil {
			if derived, ok := derivedCEP95(*o.PosCovariance); ok {
				ce = derived
			}
		}
		o.CircularError = &ce
	} else if o.PosCovariance != nil {
		if ce, ok := derivedCEP95(*o.PosCovariance); ok {
			o.CircularError = &ce
		}
	}
	if c := m.GetPositionVelocityCovariance(); c != nil {
		o.PosVelCovariance = &[9]float64{c.GetPnVn(), c.GetPnVe(), c.GetPnVu(), c.GetPeVn(), c.GetPeVe(), c.GetPeVu(), c.GetPuVn(), c.GetPuVe(), c.GetPuVu()}
	}
	if c := m.GetVelocityCovariance(); c != nil {
		o.VelCovariance = &[6]float64{c.GetVnVn(), c.GetVnVe(), c.GetVnVu(), c.GetVeVe(), c.GetVeVu(), c.GetVuVu()}
	}
	if d := m.GetDimensions(); d != nil {
		o.Dimensions = &[3]float64{d.GetLength(), d.GetWidth(), d.GetHeight()}
	}
	if p := m.GetPolar(); p != nil {
		o.Polar = &obs.Polar{
			RangeM: p.GetRangeM(), AzimuthDeg: p.GetAzimuthDeg(),
			ElevationDeg: copyFloat(p.ElevationDeg), RangeRateMps: copyFloat(p.RangeRateMps),
			AzimuthRef: enumName(p.GetAzimuthReference()),
		}
		if s := p.GetSigma(); s != nil {
			o.Polar.SigmaRangeM, o.Polar.SigmaAzimuthDeg, o.Polar.SigmaElevationDeg = copyFloat(s.RangeM), copyFloat(s.AzimuthDeg), copyFloat(s.ElevationDeg)
		}
	}
	platformFrom(o, m.GetPlatform())
	return o
}

// platformFrom is the inverse of platform: it sets the one platform detail
// the message carries.
func platformFrom(o *obs.Observation, p *sentinelv1.PlatformDetail) {
	switch d := p.GetDetail().(type) {
	case *sentinelv1.PlatformDetail_Adsb:
		a := d.Adsb
		o.ADSB = &obs.ADSBData{
			ICAOHex: a.GetIcaoHex(), Registration: a.GetRegistration(), AircraftType: a.GetAircraftType(), AircraftTypeName: a.GetAircraftTypeName(),
			OperatorICAO: a.GetOperatorIcao(), OperatorName: a.GetOperatorName(), Squawk: a.GetSquawk(), Emergency: a.GetEmergency(),
			Mode1: a.GetMode1(), Mode2: a.GetMode2(), AircraftID: a.GetAircraftId(), FlightAirborne: a.GetFlightAirborne(),
			IndicatedAirSpd: a.GetIndicatedAirSpeed(), TrueAirSpeed: a.GetTrueAirSpeed(), GroundSpeed: a.GetGroundSpeed(),
			MagneticHeading: a.GetMagneticHeading(), Mode5FigureOfMerit: int(a.GetMode5FigureOfMerit()),
			NationalOriginCode: int(a.GetNationalOriginCode()), MissionCode: int(a.GetMissionCode()), AltitudeBaro: a.GetAltitudeBaro(),
			AltitudeGeom: a.GetAltitudeGeom(), VerticalRate: a.GetVerticalRate(), OnGround: a.GetOnGround(), Category: a.GetCategory(),
			NacP: int(a.GetNacP()), NacV: int(a.GetNacV()), SIL: int(a.GetSil()), SILType: a.GetSilType(), NIC: int(a.GetNic()),
			RC: a.GetRc(), GVA: int(a.GetGva()), SDA: int(a.GetSda()),
		}
	case *sentinelv1.PlatformDetail_Ais:
		a := d.Ais
		o.AIS = &obs.AISData{
			MMSI: a.GetMmsi(), IMO: a.GetImo(), Callsign: a.GetCallsign(), VesselName: a.GetVesselName(), ShipType: int(a.GetShipType()),
			ShipTypeName: a.GetShipTypeName(), Flag: a.GetFlag(), Destination: a.GetDestination(), ETA: a.GetEta(), Draught: a.GetDraught(),
			DimensionA: a.GetDimensionA(), DimensionB: a.GetDimensionB(), DimensionC: a.GetDimensionC(), DimensionD: a.GetDimensionD(),
			LengthOverall: a.GetLengthOverall(), Beam: a.GetBeam(), NavStatus: enumName(a.GetNavStatus()), RateOfTurn: a.GetRateOfTurn(),
			SpeedOverGround: a.GetSpeedOverGround(), CourseOverGround: a.GetCourseOverGround(), TrueHeading: a.GetTrueHeading(),
			PositionAccuracyHigh: a.GetPositionAccuracyHigh(), SpecialManoeuvre: a.GetSpecialManoeuvre(),
			MessageType: int(a.GetMessageType()), RepeatIndicator: int(a.GetRepeatIndicator()),
		}
	case *sentinelv1.PlatformDetail_Tle:
		t := d.Tle
		o.TLE = &obs.TLEData{
			NoradID: int(t.GetNoradId()), IntlDesignator: t.GetIntlDesignator(), SatName: t.GetSatName(), Line1: t.GetLine1(), Line2: t.GetLine2(),
			Epoch: t.GetEpoch(), Inclination: t.GetInclination(), Eccentricity: t.GetEccentricity(), RAAN: t.GetRaan(), ArgOfPerigee: t.GetArgOfPerigee(),
			MeanAnomaly: t.GetMeanAnomaly(), MeanMotion: t.GetMeanMotion(), Period: t.GetPeriod(), Apogee: t.GetApogee(), Perigee: t.GetPerigee(),
			ObjectType: t.GetObjectType(), RCSSize: t.GetRcsSize(), LaunchDate: t.GetLaunchDate(), DecayDate: t.GetDecayDate(), Country: t.GetCountry(),
		}
	case *sentinelv1.PlatformDetail_Link16:
		l := d.Link16
		o.Link16 = &obs.Link16Data{
			TrackNumber: int(l.GetTrackNumber()), JSeriesLabel: l.GetJSeriesLabel(), OriginatingUnit: l.GetOriginatingUnit(), Quality: int(l.GetQuality()),
			ExerciseIndicator: l.GetExerciseIndicator(), SimulationIndicator: l.GetSimulationIndicator(), ForceIdentity: l.GetForceIdentity(),
		}
	case *sentinelv1.PlatformDetail_Cot:
		c := d.Cot
		o.CoT = &obs.CoTData{
			UID: c.GetUid(), CoTType: c.GetCotType(), How: c.GetHow(), CE: c.GetCe(), LE: c.GetLe(), StaleTime: c.GetStaleTime(),
			AccessControl: c.GetAccessControl(), Opex: c.GetOpex(), QOS: c.GetQos(),
		}
	case *sentinelv1.PlatformDetail_Uav:
		u := d.Uav
		o.UAV = &obs.UAVData{
			Make: u.GetMake(), Model: u.GetModel(), SerialNumber: u.GetSerialNumber(), MACAddress: u.GetMacAddress(),
			OperatingFrequency: u.GetOperatingFrequency(), FrequencyRange: u.GetFrequencyRange(),
		}
	case *sentinelv1.PlatformDetail_Rf:
		r := d.Rf
		o.RF = &obs.RFData{
			FrequencyHz: r.GetFrequencyHz(), BandwidthHz: copyFloat(r.BandwidthHz),
			EmitterType: r.GetEmitterType(), Modulation: r.GetModulation(),
			PriUs: copyFloat(r.PriUs), PulseWidthUs: copyFloat(r.PulseWidthUs),
			ScanType: r.GetScanType(), PowerDbm: copyFloat(r.PowerDbm),
		}
	}
}

func finiteCov(c [6]float64) bool {
	for _, v := range c {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func derivedCEP95(c [6]float64) (float64, bool) {
	if !finiteCov(c) || c[0] < 0 || c[3] < 0 || c[5] < 0 || (c[0] == 0 && c[3] == 0) {
		return 0, false
	}
	const maxCov = 1e14
	nn := min(c[0], maxCov)
	ee := min(c[3], maxCov)
	limit := math.Sqrt(nn * ee)
	ne := max(-limit, min(c[1], limit))
	ce := geo.CEP95(nn, ne, ee)
	if ce <= 0 || math.IsNaN(ce) || math.IsInf(ce, 0) {
		return 0, false
	}
	return ce, true
}

// enumName is the bare enum name, or "" for the zero (unspecified) value and
// for a number the enum does not declare.
func enumName(e protoreflect.Enum) string {
	if e.Number() == 0 || e.Descriptor().Values().ByNumber(e.Number()) == nil {
		return ""
	}
	return pbjson.EnumName(e)
}

func copyFloat(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
