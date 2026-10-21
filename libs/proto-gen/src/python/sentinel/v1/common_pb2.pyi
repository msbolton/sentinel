import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TrackEnvironment(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TRACK_ENVIRONMENT_UNSPECIFIED: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_AIR: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_SEA_SURFACE: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_SUBSURFACE: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_GROUND: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_SPACE: _ClassVar[TrackEnvironment]
    TRACK_ENVIRONMENT_UNKNOWN: _ClassVar[TrackEnvironment]

class TrackProcessingState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TRACK_PROCESSING_STATE_UNSPECIFIED: _ClassVar[TrackProcessingState]
    TRACK_PROCESSING_STATE_LIVE: _ClassVar[TrackProcessingState]
    TRACK_PROCESSING_STATE_PREDICTED: _ClassVar[TrackProcessingState]
    TRACK_PROCESSING_STATE_DEAD_RECKONED: _ClassVar[TrackProcessingState]
    TRACK_PROCESSING_STATE_HYPOTHESIZED: _ClassVar[TrackProcessingState]
    TRACK_PROCESSING_STATE_HISTORICAL: _ClassVar[TrackProcessingState]

class NavigationalStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    NAVIGATIONAL_STATUS_UNSPECIFIED: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_UNDER_WAY_USING_ENGINE: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_AT_ANCHOR: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_NOT_UNDER_COMMAND: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_RESTRICTED_MANOEUVRABILITY: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_CONSTRAINED_BY_DRAUGHT: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_MOORED: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_AGROUND: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_ENGAGED_IN_FISHING: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_UNDER_WAY_SAILING: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_AIS_SART: _ClassVar[NavigationalStatus]
    NAVIGATIONAL_STATUS_UNKNOWN: _ClassVar[NavigationalStatus]

class OperationalStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OPERATIONAL_STATUS_UNSPECIFIED: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_OPERATIONAL: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_DEGRADED: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_DAMAGED: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_DESTROYED: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_INACTIVE: _ClassVar[OperationalStatus]
    OPERATIONAL_STATUS_UNKNOWN: _ClassVar[OperationalStatus]

class DamageAssessment(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DAMAGE_ASSESSMENT_UNSPECIFIED: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_NONE: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_LIGHT: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_MODERATE: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_HEAVY: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_DESTROYED: _ClassVar[DamageAssessment]
    DAMAGE_ASSESSMENT_UNKNOWN: _ClassVar[DamageAssessment]

class Classification(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CLASSIFICATION_UNSPECIFIED: _ClassVar[Classification]
    CLASSIFICATION_UNCLASSIFIED: _ClassVar[Classification]
    CLASSIFICATION_CONFIDENTIAL: _ClassVar[Classification]
    CLASSIFICATION_SECRET: _ClassVar[Classification]
    CLASSIFICATION_TOP_SECRET: _ClassVar[Classification]

class IdentityType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    IDENTITY_TYPE_UNSPECIFIED: _ClassVar[IdentityType]
    IDENTITY_TYPE_ICAO24: _ClassVar[IdentityType]
    IDENTITY_TYPE_MMSI: _ClassVar[IdentityType]
    IDENTITY_TYPE_IMO: _ClassVar[IdentityType]
    IDENTITY_TYPE_NORAD_ID: _ClassVar[IdentityType]
    IDENTITY_TYPE_COT_UID: _ClassVar[IdentityType]
    IDENTITY_TYPE_LINK16_TN: _ClassVar[IdentityType]
    IDENTITY_TYPE_FEED_LOCAL: _ClassVar[IdentityType]
    IDENTITY_TYPE_CALLSIGN: _ClassVar[IdentityType]
    IDENTITY_TYPE_REGISTRATION: _ClassVar[IdentityType]
    IDENTITY_TYPE_EMPLACEMENT: _ClassVar[IdentityType]

class AgeoutState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    AGEOUT_STATE_UNSPECIFIED: _ClassVar[AgeoutState]
    AGEOUT_STATE_LIVE: _ClassVar[AgeoutState]
    AGEOUT_STATE_STALE: _ClassVar[AgeoutState]
    AGEOUT_STATE_AGED_OUT: _ClassVar[AgeoutState]
TRACK_ENVIRONMENT_UNSPECIFIED: TrackEnvironment
TRACK_ENVIRONMENT_AIR: TrackEnvironment
TRACK_ENVIRONMENT_SEA_SURFACE: TrackEnvironment
TRACK_ENVIRONMENT_SUBSURFACE: TrackEnvironment
TRACK_ENVIRONMENT_GROUND: TrackEnvironment
TRACK_ENVIRONMENT_SPACE: TrackEnvironment
TRACK_ENVIRONMENT_UNKNOWN: TrackEnvironment
TRACK_PROCESSING_STATE_UNSPECIFIED: TrackProcessingState
TRACK_PROCESSING_STATE_LIVE: TrackProcessingState
TRACK_PROCESSING_STATE_PREDICTED: TrackProcessingState
TRACK_PROCESSING_STATE_DEAD_RECKONED: TrackProcessingState
TRACK_PROCESSING_STATE_HYPOTHESIZED: TrackProcessingState
TRACK_PROCESSING_STATE_HISTORICAL: TrackProcessingState
NAVIGATIONAL_STATUS_UNSPECIFIED: NavigationalStatus
NAVIGATIONAL_STATUS_UNDER_WAY_USING_ENGINE: NavigationalStatus
NAVIGATIONAL_STATUS_AT_ANCHOR: NavigationalStatus
NAVIGATIONAL_STATUS_NOT_UNDER_COMMAND: NavigationalStatus
NAVIGATIONAL_STATUS_RESTRICTED_MANOEUVRABILITY: NavigationalStatus
NAVIGATIONAL_STATUS_CONSTRAINED_BY_DRAUGHT: NavigationalStatus
NAVIGATIONAL_STATUS_MOORED: NavigationalStatus
NAVIGATIONAL_STATUS_AGROUND: NavigationalStatus
NAVIGATIONAL_STATUS_ENGAGED_IN_FISHING: NavigationalStatus
NAVIGATIONAL_STATUS_UNDER_WAY_SAILING: NavigationalStatus
NAVIGATIONAL_STATUS_AIS_SART: NavigationalStatus
NAVIGATIONAL_STATUS_UNKNOWN: NavigationalStatus
OPERATIONAL_STATUS_UNSPECIFIED: OperationalStatus
OPERATIONAL_STATUS_OPERATIONAL: OperationalStatus
OPERATIONAL_STATUS_DEGRADED: OperationalStatus
OPERATIONAL_STATUS_DAMAGED: OperationalStatus
OPERATIONAL_STATUS_DESTROYED: OperationalStatus
OPERATIONAL_STATUS_INACTIVE: OperationalStatus
OPERATIONAL_STATUS_UNKNOWN: OperationalStatus
DAMAGE_ASSESSMENT_UNSPECIFIED: DamageAssessment
DAMAGE_ASSESSMENT_NONE: DamageAssessment
DAMAGE_ASSESSMENT_LIGHT: DamageAssessment
DAMAGE_ASSESSMENT_MODERATE: DamageAssessment
DAMAGE_ASSESSMENT_HEAVY: DamageAssessment
DAMAGE_ASSESSMENT_DESTROYED: DamageAssessment
DAMAGE_ASSESSMENT_UNKNOWN: DamageAssessment
CLASSIFICATION_UNSPECIFIED: Classification
CLASSIFICATION_UNCLASSIFIED: Classification
CLASSIFICATION_CONFIDENTIAL: Classification
CLASSIFICATION_SECRET: Classification
CLASSIFICATION_TOP_SECRET: Classification
IDENTITY_TYPE_UNSPECIFIED: IdentityType
IDENTITY_TYPE_ICAO24: IdentityType
IDENTITY_TYPE_MMSI: IdentityType
IDENTITY_TYPE_IMO: IdentityType
IDENTITY_TYPE_NORAD_ID: IdentityType
IDENTITY_TYPE_COT_UID: IdentityType
IDENTITY_TYPE_LINK16_TN: IdentityType
IDENTITY_TYPE_FEED_LOCAL: IdentityType
IDENTITY_TYPE_CALLSIGN: IdentityType
IDENTITY_TYPE_REGISTRATION: IdentityType
IDENTITY_TYPE_EMPLACEMENT: IdentityType
AGEOUT_STATE_UNSPECIFIED: AgeoutState
AGEOUT_STATE_LIVE: AgeoutState
AGEOUT_STATE_STALE: AgeoutState
AGEOUT_STATE_AGED_OUT: AgeoutState

class UUID(_message.Message):
    __slots__ = ("value",)
    VALUE_FIELD_NUMBER: _ClassVar[int]
    value: str
    def __init__(self, value: _Optional[str] = ...) -> None: ...

class Coordinate(_message.Message):
    __slots__ = ("latitude", "longitude", "altitude_meters", "coordinate_system")
    LATITUDE_FIELD_NUMBER: _ClassVar[int]
    LONGITUDE_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_METERS_FIELD_NUMBER: _ClassVar[int]
    COORDINATE_SYSTEM_FIELD_NUMBER: _ClassVar[int]
    latitude: float
    longitude: float
    altitude_meters: float
    coordinate_system: str
    def __init__(self, latitude: _Optional[float] = ..., longitude: _Optional[float] = ..., altitude_meters: _Optional[float] = ..., coordinate_system: _Optional[str] = ...) -> None: ...

class BoundingBox(_message.Message):
    __slots__ = ("north", "south", "east", "west")
    NORTH_FIELD_NUMBER: _ClassVar[int]
    SOUTH_FIELD_NUMBER: _ClassVar[int]
    EAST_FIELD_NUMBER: _ClassVar[int]
    WEST_FIELD_NUMBER: _ClassVar[int]
    north: float
    south: float
    east: float
    west: float
    def __init__(self, north: _Optional[float] = ..., south: _Optional[float] = ..., east: _Optional[float] = ..., west: _Optional[float] = ...) -> None: ...

class Timestamp(_message.Message):
    __slots__ = ("value",)
    VALUE_FIELD_NUMBER: _ClassVar[int]
    value: _timestamp_pb2.Timestamp
    def __init__(self, value: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Pagination(_message.Message):
    __slots__ = ("page", "page_size", "total")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    page: int
    page_size: int
    total: int
    def __init__(self, page: _Optional[int] = ..., page_size: _Optional[int] = ..., total: _Optional[int] = ...) -> None: ...

class Identity(_message.Message):
    __slots__ = ("type", "value")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    type: IdentityType
    value: str
    def __init__(self, type: _Optional[_Union[IdentityType, str]] = ..., value: _Optional[str] = ...) -> None: ...
