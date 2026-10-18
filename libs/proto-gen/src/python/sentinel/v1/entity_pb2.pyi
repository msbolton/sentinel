import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import platform_pb2 as _platform_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class EntityType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ENTITY_TYPE_UNKNOWN: _ClassVar[EntityType]
    ENTITY_TYPE_PERSON: _ClassVar[EntityType]
    ENTITY_TYPE_VEHICLE: _ClassVar[EntityType]
    ENTITY_TYPE_VESSEL: _ClassVar[EntityType]
    ENTITY_TYPE_AIRCRAFT: _ClassVar[EntityType]
    ENTITY_TYPE_FACILITY: _ClassVar[EntityType]
    ENTITY_TYPE_EQUIPMENT: _ClassVar[EntityType]
    ENTITY_TYPE_UNIT: _ClassVar[EntityType]
    ENTITY_TYPE_SIGNAL: _ClassVar[EntityType]
    ENTITY_TYPE_CYBER: _ClassVar[EntityType]
    ENTITY_TYPE_SENSOR: _ClassVar[EntityType]
    ENTITY_TYPE_SATELLITE: _ClassVar[EntityType]
    ENTITY_TYPE_DRONE: _ClassVar[EntityType]

class EntitySource(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ENTITY_SOURCE_UNSPECIFIED: _ClassVar[EntitySource]
    ENTITY_SOURCE_HUMINT: _ClassVar[EntitySource]
    ENTITY_SOURCE_SIGINT: _ClassVar[EntitySource]
    ENTITY_SOURCE_GEOINT: _ClassVar[EntitySource]
    ENTITY_SOURCE_OSINT: _ClassVar[EntitySource]
    ENTITY_SOURCE_MASINT: _ClassVar[EntitySource]
    ENTITY_SOURCE_CYBER: _ClassVar[EntitySource]
    ENTITY_SOURCE_MANUAL: _ClassVar[EntitySource]
    ENTITY_SOURCE_AIS: _ClassVar[EntitySource]
    ENTITY_SOURCE_ADS_B: _ClassVar[EntitySource]
    ENTITY_SOURCE_LINK16: _ClassVar[EntitySource]
    ENTITY_SOURCE_GPS: _ClassVar[EntitySource]
    ENTITY_SOURCE_RADAR: _ClassVar[EntitySource]
    ENTITY_SOURCE_CELESTRAK: _ClassVar[EntitySource]
    ENTITY_SOURCE_OPENSKY: _ClassVar[EntitySource]
    ENTITY_SOURCE_ADSB_LOL: _ClassVar[EntitySource]
    ENTITY_SOURCE_INTELSKY: _ClassVar[EntitySource]

class Affiliation(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    AFFILIATION_UNSPECIFIED: _ClassVar[Affiliation]
    AFFILIATION_FRIENDLY: _ClassVar[Affiliation]
    AFFILIATION_HOSTILE: _ClassVar[Affiliation]
    AFFILIATION_NEUTRAL: _ClassVar[Affiliation]
    AFFILIATION_UNKNOWN: _ClassVar[Affiliation]
    AFFILIATION_ASSUMED_FRIENDLY: _ClassVar[Affiliation]
    AFFILIATION_SUSPECT: _ClassVar[Affiliation]
    AFFILIATION_PENDING: _ClassVar[Affiliation]

class CharacterizationState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CHARACTERIZATION_STATE_UNSPECIFIED: _ClassVar[CharacterizationState]
    CHARACTERIZATION_STATE_ASSESSED: _ClassVar[CharacterizationState]
    CHARACTERIZATION_STATE_ASSUMED: _ClassVar[CharacterizationState]
    CHARACTERIZATION_STATE_SUSPECTED: _ClassVar[CharacterizationState]
    CHARACTERIZATION_STATE_UNCHARACTERIZED: _ClassVar[CharacterizationState]
ENTITY_TYPE_UNKNOWN: EntityType
ENTITY_TYPE_PERSON: EntityType
ENTITY_TYPE_VEHICLE: EntityType
ENTITY_TYPE_VESSEL: EntityType
ENTITY_TYPE_AIRCRAFT: EntityType
ENTITY_TYPE_FACILITY: EntityType
ENTITY_TYPE_EQUIPMENT: EntityType
ENTITY_TYPE_UNIT: EntityType
ENTITY_TYPE_SIGNAL: EntityType
ENTITY_TYPE_CYBER: EntityType
ENTITY_TYPE_SENSOR: EntityType
ENTITY_TYPE_SATELLITE: EntityType
ENTITY_TYPE_DRONE: EntityType
ENTITY_SOURCE_UNSPECIFIED: EntitySource
ENTITY_SOURCE_HUMINT: EntitySource
ENTITY_SOURCE_SIGINT: EntitySource
ENTITY_SOURCE_GEOINT: EntitySource
ENTITY_SOURCE_OSINT: EntitySource
ENTITY_SOURCE_MASINT: EntitySource
ENTITY_SOURCE_CYBER: EntitySource
ENTITY_SOURCE_MANUAL: EntitySource
ENTITY_SOURCE_AIS: EntitySource
ENTITY_SOURCE_ADS_B: EntitySource
ENTITY_SOURCE_LINK16: EntitySource
ENTITY_SOURCE_GPS: EntitySource
ENTITY_SOURCE_RADAR: EntitySource
ENTITY_SOURCE_CELESTRAK: EntitySource
ENTITY_SOURCE_OPENSKY: EntitySource
ENTITY_SOURCE_ADSB_LOL: EntitySource
ENTITY_SOURCE_INTELSKY: EntitySource
AFFILIATION_UNSPECIFIED: Affiliation
AFFILIATION_FRIENDLY: Affiliation
AFFILIATION_HOSTILE: Affiliation
AFFILIATION_NEUTRAL: Affiliation
AFFILIATION_UNKNOWN: Affiliation
AFFILIATION_ASSUMED_FRIENDLY: Affiliation
AFFILIATION_SUSPECT: Affiliation
AFFILIATION_PENDING: Affiliation
CHARACTERIZATION_STATE_UNSPECIFIED: CharacterizationState
CHARACTERIZATION_STATE_ASSESSED: CharacterizationState
CHARACTERIZATION_STATE_ASSUMED: CharacterizationState
CHARACTERIZATION_STATE_SUSPECTED: CharacterizationState
CHARACTERIZATION_STATE_UNCHARACTERIZED: CharacterizationState

class Orientation(_message.Message):
    __slots__ = ("yaw", "pitch", "roll")
    YAW_FIELD_NUMBER: _ClassVar[int]
    PITCH_FIELD_NUMBER: _ClassVar[int]
    ROLL_FIELD_NUMBER: _ClassVar[int]
    yaw: float
    pitch: float
    roll: float
    def __init__(self, yaw: _Optional[float] = ..., pitch: _Optional[float] = ..., roll: _Optional[float] = ...) -> None: ...

class GeodeticVelocity(_message.Message):
    __slots__ = ("north", "east", "up")
    NORTH_FIELD_NUMBER: _ClassVar[int]
    EAST_FIELD_NUMBER: _ClassVar[int]
    UP_FIELD_NUMBER: _ClassVar[int]
    north: float
    east: float
    up: float
    def __init__(self, north: _Optional[float] = ..., east: _Optional[float] = ..., up: _Optional[float] = ...) -> None: ...

class GeodeticAcceleration(_message.Message):
    __slots__ = ("north", "east", "up")
    NORTH_FIELD_NUMBER: _ClassVar[int]
    EAST_FIELD_NUMBER: _ClassVar[int]
    UP_FIELD_NUMBER: _ClassVar[int]
    north: float
    east: float
    up: float
    def __init__(self, north: _Optional[float] = ..., east: _Optional[float] = ..., up: _Optional[float] = ...) -> None: ...

class PositionCovariance(_message.Message):
    __slots__ = ("pn_pn", "pn_pe", "pn_pu", "pe_pe", "pe_pu", "pu_pu")
    PN_PN_FIELD_NUMBER: _ClassVar[int]
    PN_PE_FIELD_NUMBER: _ClassVar[int]
    PN_PU_FIELD_NUMBER: _ClassVar[int]
    PE_PE_FIELD_NUMBER: _ClassVar[int]
    PE_PU_FIELD_NUMBER: _ClassVar[int]
    PU_PU_FIELD_NUMBER: _ClassVar[int]
    pn_pn: float
    pn_pe: float
    pn_pu: float
    pe_pe: float
    pe_pu: float
    pu_pu: float
    def __init__(self, pn_pn: _Optional[float] = ..., pn_pe: _Optional[float] = ..., pn_pu: _Optional[float] = ..., pe_pe: _Optional[float] = ..., pe_pu: _Optional[float] = ..., pu_pu: _Optional[float] = ...) -> None: ...

class PositionVelocityCovariance(_message.Message):
    __slots__ = ("pn_vn", "pn_ve", "pn_vu", "pe_vn", "pe_ve", "pe_vu", "pu_vn", "pu_ve", "pu_vu")
    PN_VN_FIELD_NUMBER: _ClassVar[int]
    PN_VE_FIELD_NUMBER: _ClassVar[int]
    PN_VU_FIELD_NUMBER: _ClassVar[int]
    PE_VN_FIELD_NUMBER: _ClassVar[int]
    PE_VE_FIELD_NUMBER: _ClassVar[int]
    PE_VU_FIELD_NUMBER: _ClassVar[int]
    PU_VN_FIELD_NUMBER: _ClassVar[int]
    PU_VE_FIELD_NUMBER: _ClassVar[int]
    PU_VU_FIELD_NUMBER: _ClassVar[int]
    pn_vn: float
    pn_ve: float
    pn_vu: float
    pe_vn: float
    pe_ve: float
    pe_vu: float
    pu_vn: float
    pu_ve: float
    pu_vu: float
    def __init__(self, pn_vn: _Optional[float] = ..., pn_ve: _Optional[float] = ..., pn_vu: _Optional[float] = ..., pe_vn: _Optional[float] = ..., pe_ve: _Optional[float] = ..., pe_vu: _Optional[float] = ..., pu_vn: _Optional[float] = ..., pu_ve: _Optional[float] = ..., pu_vu: _Optional[float] = ...) -> None: ...

class VelocityCovariance(_message.Message):
    __slots__ = ("vn_vn", "vn_ve", "vn_vu", "ve_ve", "ve_vu", "vu_vu")
    VN_VN_FIELD_NUMBER: _ClassVar[int]
    VN_VE_FIELD_NUMBER: _ClassVar[int]
    VN_VU_FIELD_NUMBER: _ClassVar[int]
    VE_VE_FIELD_NUMBER: _ClassVar[int]
    VE_VU_FIELD_NUMBER: _ClassVar[int]
    VU_VU_FIELD_NUMBER: _ClassVar[int]
    vn_vn: float
    vn_ve: float
    vn_vu: float
    ve_ve: float
    ve_vu: float
    vu_vu: float
    def __init__(self, vn_vn: _Optional[float] = ..., vn_ve: _Optional[float] = ..., vn_vu: _Optional[float] = ..., ve_ve: _Optional[float] = ..., ve_vu: _Optional[float] = ..., vu_vu: _Optional[float] = ...) -> None: ...

class KinematicState(_message.Message):
    __slots__ = ("velocity", "acceleration", "position_covariance", "position_velocity_covariance", "velocity_covariance")
    VELOCITY_FIELD_NUMBER: _ClassVar[int]
    ACCELERATION_FIELD_NUMBER: _ClassVar[int]
    POSITION_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    POSITION_VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    velocity: GeodeticVelocity
    acceleration: GeodeticAcceleration
    position_covariance: PositionCovariance
    position_velocity_covariance: PositionVelocityCovariance
    velocity_covariance: VelocityCovariance
    def __init__(self, velocity: _Optional[_Union[GeodeticVelocity, _Mapping]] = ..., acceleration: _Optional[_Union[GeodeticAcceleration, _Mapping]] = ..., position_covariance: _Optional[_Union[PositionCovariance, _Mapping]] = ..., position_velocity_covariance: _Optional[_Union[PositionVelocityCovariance, _Mapping]] = ..., velocity_covariance: _Optional[_Union[VelocityCovariance, _Mapping]] = ...) -> None: ...

class MeasurementUncertainty(_message.Message):
    __slots__ = ("circular_error", "semi_major", "semi_minor", "orientation", "altitude_error", "confidence")
    CIRCULAR_ERROR_FIELD_NUMBER: _ClassVar[int]
    SEMI_MAJOR_FIELD_NUMBER: _ClassVar[int]
    SEMI_MINOR_FIELD_NUMBER: _ClassVar[int]
    ORIENTATION_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_ERROR_FIELD_NUMBER: _ClassVar[int]
    CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    circular_error: float
    semi_major: float
    semi_minor: float
    orientation: float
    altitude_error: float
    confidence: float
    def __init__(self, circular_error: _Optional[float] = ..., semi_major: _Optional[float] = ..., semi_minor: _Optional[float] = ..., orientation: _Optional[float] = ..., altitude_error: _Optional[float] = ..., confidence: _Optional[float] = ...) -> None: ...

class Dimensions(_message.Message):
    __slots__ = ("length", "width", "height")
    LENGTH_FIELD_NUMBER: _ClassVar[int]
    WIDTH_FIELD_NUMBER: _ClassVar[int]
    HEIGHT_FIELD_NUMBER: _ClassVar[int]
    length: float
    width: float
    height: float
    def __init__(self, length: _Optional[float] = ..., width: _Optional[float] = ..., height: _Optional[float] = ...) -> None: ...

class Entity(_message.Message):
    __slots__ = ("id", "entity_type", "name", "description", "source", "classification", "position", "heading", "speed_knots", "course", "metadata", "affiliations", "mil_std_2525d_symbol", "created_at", "updated_at", "last_seen_at", "affiliation", "identity_confidence", "characterization", "track_environment", "track_processing_state", "orientation", "kinematics", "operational_status", "damage_assessment", "damage_confidence", "dimensions", "country_of_origin", "source_entity_id", "circular_error", "last_observation_source", "feed_id", "last_feed_id", "ageout_state", "identities", "sources", "last_observed_at", "platforms")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    class PlatformsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: _platform_pb2.PlatformDetail
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[_platform_pb2.PlatformDetail, _Mapping]] = ...) -> None: ...
    ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    AFFILIATIONS_FIELD_NUMBER: _ClassVar[int]
    MIL_STD_2525D_SYMBOL_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_AT_FIELD_NUMBER: _ClassVar[int]
    AFFILIATION_FIELD_NUMBER: _ClassVar[int]
    IDENTITY_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    CHARACTERIZATION_FIELD_NUMBER: _ClassVar[int]
    TRACK_ENVIRONMENT_FIELD_NUMBER: _ClassVar[int]
    TRACK_PROCESSING_STATE_FIELD_NUMBER: _ClassVar[int]
    ORIENTATION_FIELD_NUMBER: _ClassVar[int]
    KINEMATICS_FIELD_NUMBER: _ClassVar[int]
    OPERATIONAL_STATUS_FIELD_NUMBER: _ClassVar[int]
    DAMAGE_ASSESSMENT_FIELD_NUMBER: _ClassVar[int]
    DAMAGE_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    COUNTRY_OF_ORIGIN_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    CIRCULAR_ERROR_FIELD_NUMBER: _ClassVar[int]
    LAST_OBSERVATION_SOURCE_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    LAST_FEED_ID_FIELD_NUMBER: _ClassVar[int]
    AGEOUT_STATE_FIELD_NUMBER: _ClassVar[int]
    IDENTITIES_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    LAST_OBSERVED_AT_FIELD_NUMBER: _ClassVar[int]
    PLATFORMS_FIELD_NUMBER: _ClassVar[int]
    id: str
    entity_type: EntityType
    name: str
    description: str
    source: EntitySource
    classification: _common_pb2.Classification
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    metadata: _containers.ScalarMap[str, str]
    affiliations: _containers.RepeatedScalarFieldContainer[str]
    mil_std_2525d_symbol: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    last_seen_at: _timestamp_pb2.Timestamp
    affiliation: Affiliation
    identity_confidence: int
    characterization: CharacterizationState
    track_environment: _common_pb2.TrackEnvironment
    track_processing_state: _common_pb2.TrackProcessingState
    orientation: Orientation
    kinematics: KinematicState
    operational_status: _common_pb2.OperationalStatus
    damage_assessment: _common_pb2.DamageAssessment
    damage_confidence: int
    dimensions: Dimensions
    country_of_origin: str
    source_entity_id: str
    circular_error: float
    last_observation_source: str
    feed_id: str
    last_feed_id: str
    ageout_state: _common_pb2.AgeoutState
    identities: _containers.RepeatedCompositeFieldContainer[_common_pb2.Identity]
    sources: _containers.RepeatedCompositeFieldContainer[SourceContribution]
    last_observed_at: _timestamp_pb2.Timestamp
    platforms: _containers.MessageMap[str, _platform_pb2.PlatformDetail]
    def __init__(self, id: _Optional[str] = ..., entity_type: _Optional[_Union[EntityType, str]] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., source: _Optional[_Union[EntitySource, str]] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., metadata: _Optional[_Mapping[str, str]] = ..., affiliations: _Optional[_Iterable[str]] = ..., mil_std_2525d_symbol: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_seen_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., affiliation: _Optional[_Union[Affiliation, str]] = ..., identity_confidence: _Optional[int] = ..., characterization: _Optional[_Union[CharacterizationState, str]] = ..., track_environment: _Optional[_Union[_common_pb2.TrackEnvironment, str]] = ..., track_processing_state: _Optional[_Union[_common_pb2.TrackProcessingState, str]] = ..., orientation: _Optional[_Union[Orientation, _Mapping]] = ..., kinematics: _Optional[_Union[KinematicState, _Mapping]] = ..., operational_status: _Optional[_Union[_common_pb2.OperationalStatus, str]] = ..., damage_assessment: _Optional[_Union[_common_pb2.DamageAssessment, str]] = ..., damage_confidence: _Optional[int] = ..., dimensions: _Optional[_Union[Dimensions, _Mapping]] = ..., country_of_origin: _Optional[str] = ..., source_entity_id: _Optional[str] = ..., circular_error: _Optional[float] = ..., last_observation_source: _Optional[str] = ..., feed_id: _Optional[str] = ..., last_feed_id: _Optional[str] = ..., ageout_state: _Optional[_Union[_common_pb2.AgeoutState, str]] = ..., identities: _Optional[_Iterable[_Union[_common_pb2.Identity, _Mapping]]] = ..., sources: _Optional[_Iterable[_Union[SourceContribution, _Mapping]]] = ..., last_observed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., platforms: _Optional[_Mapping[str, _platform_pb2.PlatformDetail]] = ...) -> None: ...

class SourceContribution(_message.Message):
    __slots__ = ("feed_id", "source", "source_entity_id", "last_observed_at", "last_received_at", "observation_count")
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    LAST_OBSERVED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_RECEIVED_AT_FIELD_NUMBER: _ClassVar[int]
    OBSERVATION_COUNT_FIELD_NUMBER: _ClassVar[int]
    feed_id: str
    source: EntitySource
    source_entity_id: str
    last_observed_at: _timestamp_pb2.Timestamp
    last_received_at: _timestamp_pb2.Timestamp
    observation_count: int
    def __init__(self, feed_id: _Optional[str] = ..., source: _Optional[_Union[EntitySource, str]] = ..., source_entity_id: _Optional[str] = ..., last_observed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_received_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., observation_count: _Optional[int] = ...) -> None: ...

class CreateEntityRequest(_message.Message):
    __slots__ = ("entity_type", "name", "description", "source", "classification", "position", "heading", "speed_knots", "course", "metadata", "affiliations", "mil_std_2525d_symbol")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    AFFILIATIONS_FIELD_NUMBER: _ClassVar[int]
    MIL_STD_2525D_SYMBOL_FIELD_NUMBER: _ClassVar[int]
    entity_type: EntityType
    name: str
    description: str
    source: EntitySource
    classification: _common_pb2.Classification
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    metadata: _containers.ScalarMap[str, str]
    affiliations: _containers.RepeatedScalarFieldContainer[str]
    mil_std_2525d_symbol: str
    def __init__(self, entity_type: _Optional[_Union[EntityType, str]] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., source: _Optional[_Union[EntitySource, str]] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., metadata: _Optional[_Mapping[str, str]] = ..., affiliations: _Optional[_Iterable[str]] = ..., mil_std_2525d_symbol: _Optional[str] = ...) -> None: ...

class CreateEntityResponse(_message.Message):
    __slots__ = ("entity",)
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    entity: Entity
    def __init__(self, entity: _Optional[_Union[Entity, _Mapping]] = ...) -> None: ...

class UpdateEntityRequest(_message.Message):
    __slots__ = ("id", "entity_type", "name", "description", "source", "classification", "position", "heading", "speed_knots", "course", "metadata", "affiliations", "mil_std_2525d_symbol")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    AFFILIATIONS_FIELD_NUMBER: _ClassVar[int]
    MIL_STD_2525D_SYMBOL_FIELD_NUMBER: _ClassVar[int]
    id: str
    entity_type: EntityType
    name: str
    description: str
    source: EntitySource
    classification: _common_pb2.Classification
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    metadata: _containers.ScalarMap[str, str]
    affiliations: _containers.RepeatedScalarFieldContainer[str]
    mil_std_2525d_symbol: str
    def __init__(self, id: _Optional[str] = ..., entity_type: _Optional[_Union[EntityType, str]] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., source: _Optional[_Union[EntitySource, str]] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., metadata: _Optional[_Mapping[str, str]] = ..., affiliations: _Optional[_Iterable[str]] = ..., mil_std_2525d_symbol: _Optional[str] = ...) -> None: ...

class UpdateEntityResponse(_message.Message):
    __slots__ = ("entity",)
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    entity: Entity
    def __init__(self, entity: _Optional[_Union[Entity, _Mapping]] = ...) -> None: ...

class GetEntityRequest(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class GetEntityResponse(_message.Message):
    __slots__ = ("entity",)
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    entity: Entity
    def __init__(self, entity: _Optional[_Union[Entity, _Mapping]] = ...) -> None: ...

class QueryEntitiesRequest(_message.Message):
    __slots__ = ("bounding_box", "entity_types", "sources", "classification_max", "text_query", "pagination")
    BOUNDING_BOX_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPES_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_MAX_FIELD_NUMBER: _ClassVar[int]
    TEXT_QUERY_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    bounding_box: _common_pb2.BoundingBox
    entity_types: _containers.RepeatedScalarFieldContainer[EntityType]
    sources: _containers.RepeatedScalarFieldContainer[EntitySource]
    classification_max: _common_pb2.Classification
    text_query: str
    pagination: _common_pb2.Pagination
    def __init__(self, bounding_box: _Optional[_Union[_common_pb2.BoundingBox, _Mapping]] = ..., entity_types: _Optional[_Iterable[_Union[EntityType, str]]] = ..., sources: _Optional[_Iterable[_Union[EntitySource, str]]] = ..., classification_max: _Optional[_Union[_common_pb2.Classification, str]] = ..., text_query: _Optional[str] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...

class QueryEntitiesResponse(_message.Message):
    __slots__ = ("entities", "pagination")
    ENTITIES_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    entities: _containers.RepeatedCompositeFieldContainer[Entity]
    pagination: _common_pb2.Pagination
    def __init__(self, entities: _Optional[_Iterable[_Union[Entity, _Mapping]]] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...
