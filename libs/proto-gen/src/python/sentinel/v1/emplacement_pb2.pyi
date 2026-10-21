import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import entity_pb2 as _entity_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Mobility(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MOBILITY_UNSPECIFIED: _ClassVar[Mobility]
    MOBILITY_FIXED: _ClassVar[Mobility]
    MOBILITY_MOBILE: _ClassVar[Mobility]

class SensorType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SENSOR_TYPE_UNSPECIFIED: _ClassVar[SensorType]
    SENSOR_TYPE_RADAR: _ClassVar[SensorType]
    SENSOR_TYPE_EO_IR: _ClassVar[SensorType]
    SENSOR_TYPE_RF: _ClassVar[SensorType]
    SENSOR_TYPE_ACOUSTIC: _ClassVar[SensorType]
    SENSOR_TYPE_AIS_RECEIVER: _ClassVar[SensorType]
    SENSOR_TYPE_ADSB_RECEIVER: _ClassVar[SensorType]
    SENSOR_TYPE_HUMAN: _ClassVar[SensorType]
    SENSOR_TYPE_OTHER: _ClassVar[SensorType]

class AzimuthReference(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    AZIMUTH_REFERENCE_UNSPECIFIED: _ClassVar[AzimuthReference]
    AZIMUTH_REFERENCE_TRUE_NORTH: _ClassVar[AzimuthReference]
    AZIMUTH_REFERENCE_PLATFORM: _ClassVar[AzimuthReference]
MOBILITY_UNSPECIFIED: Mobility
MOBILITY_FIXED: Mobility
MOBILITY_MOBILE: Mobility
SENSOR_TYPE_UNSPECIFIED: SensorType
SENSOR_TYPE_RADAR: SensorType
SENSOR_TYPE_EO_IR: SensorType
SENSOR_TYPE_RF: SensorType
SENSOR_TYPE_ACOUSTIC: SensorType
SENSOR_TYPE_AIS_RECEIVER: SensorType
SENSOR_TYPE_ADSB_RECEIVER: SensorType
SENSOR_TYPE_HUMAN: SensorType
SENSOR_TYPE_OTHER: SensorType
AZIMUTH_REFERENCE_UNSPECIFIED: AzimuthReference
AZIMUTH_REFERENCE_TRUE_NORTH: AzimuthReference
AZIMUTH_REFERENCE_PLATFORM: AzimuthReference

class Coverage(_message.Message):
    __slots__ = ("min_range_m", "max_range_m", "azimuth_width_deg", "elevation_min_deg", "elevation_max_deg")
    MIN_RANGE_M_FIELD_NUMBER: _ClassVar[int]
    MAX_RANGE_M_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_WIDTH_DEG_FIELD_NUMBER: _ClassVar[int]
    ELEVATION_MIN_DEG_FIELD_NUMBER: _ClassVar[int]
    ELEVATION_MAX_DEG_FIELD_NUMBER: _ClassVar[int]
    min_range_m: float
    max_range_m: float
    azimuth_width_deg: float
    elevation_min_deg: float
    elevation_max_deg: float
    def __init__(self, min_range_m: _Optional[float] = ..., max_range_m: _Optional[float] = ..., azimuth_width_deg: _Optional[float] = ..., elevation_min_deg: _Optional[float] = ..., elevation_max_deg: _Optional[float] = ...) -> None: ...

class PolarSigma(_message.Message):
    __slots__ = ("range_m", "azimuth_deg", "elevation_deg")
    RANGE_M_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_DEG_FIELD_NUMBER: _ClassVar[int]
    ELEVATION_DEG_FIELD_NUMBER: _ClassVar[int]
    range_m: float
    azimuth_deg: float
    elevation_deg: float
    def __init__(self, range_m: _Optional[float] = ..., azimuth_deg: _Optional[float] = ..., elevation_deg: _Optional[float] = ...) -> None: ...

class PolarMeasurement(_message.Message):
    __slots__ = ("range_m", "azimuth_deg", "elevation_deg", "range_rate_mps", "azimuth_reference", "sigma")
    RANGE_M_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_DEG_FIELD_NUMBER: _ClassVar[int]
    ELEVATION_DEG_FIELD_NUMBER: _ClassVar[int]
    RANGE_RATE_MPS_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_REFERENCE_FIELD_NUMBER: _ClassVar[int]
    SIGMA_FIELD_NUMBER: _ClassVar[int]
    range_m: float
    azimuth_deg: float
    elevation_deg: float
    range_rate_mps: float
    azimuth_reference: AzimuthReference
    sigma: PolarSigma
    def __init__(self, range_m: _Optional[float] = ..., azimuth_deg: _Optional[float] = ..., elevation_deg: _Optional[float] = ..., range_rate_mps: _Optional[float] = ..., azimuth_reference: _Optional[_Union[AzimuthReference, str]] = ..., sigma: _Optional[_Union[PolarSigma, _Mapping]] = ...) -> None: ...

class Emplacement(_message.Message):
    __slots__ = ("id", "name", "description", "mobility", "sensor_type", "position", "boresight_deg", "azimuth_reference", "coverage", "default_sigma", "classification", "exercise", "feed_id", "active", "updated_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    MOBILITY_FIELD_NUMBER: _ClassVar[int]
    SENSOR_TYPE_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    BORESIGHT_DEG_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_REFERENCE_FIELD_NUMBER: _ClassVar[int]
    COVERAGE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_SIGMA_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    EXERCISE_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    description: str
    mobility: Mobility
    sensor_type: SensorType
    position: _common_pb2.Coordinate
    boresight_deg: float
    azimuth_reference: AzimuthReference
    coverage: Coverage
    default_sigma: PolarSigma
    classification: _common_pb2.Classification
    exercise: bool
    feed_id: str
    active: bool
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., mobility: _Optional[_Union[Mobility, str]] = ..., sensor_type: _Optional[_Union[SensorType, str]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., boresight_deg: _Optional[float] = ..., azimuth_reference: _Optional[_Union[AzimuthReference, str]] = ..., coverage: _Optional[_Union[Coverage, _Mapping]] = ..., default_sigma: _Optional[_Union[PolarSigma, _Mapping]] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ..., exercise: _Optional[bool] = ..., feed_id: _Optional[str] = ..., active: _Optional[bool] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class EmplacementState(_message.Message):
    __slots__ = ("emplacement_id", "time", "position", "heading", "velocity", "orientation", "heartbeat_interval_sec")
    EMPLACEMENT_ID_FIELD_NUMBER: _ClassVar[int]
    TIME_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_FIELD_NUMBER: _ClassVar[int]
    ORIENTATION_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_INTERVAL_SEC_FIELD_NUMBER: _ClassVar[int]
    emplacement_id: str
    time: _timestamp_pb2.Timestamp
    position: _common_pb2.Coordinate
    heading: float
    velocity: _entity_pb2.GeodeticVelocity
    orientation: _entity_pb2.Orientation
    heartbeat_interval_sec: int
    def __init__(self, emplacement_id: _Optional[str] = ..., time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., velocity: _Optional[_Union[_entity_pb2.GeodeticVelocity, _Mapping]] = ..., orientation: _Optional[_Union[_entity_pb2.Orientation, _Mapping]] = ..., heartbeat_interval_sec: _Optional[int] = ...) -> None: ...
