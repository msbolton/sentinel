import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import entity_pb2 as _entity_pb2
from sentinel.v1 import platform_pb2 as _platform_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RawPayload(_message.Message):
    __slots__ = ("content_type", "data", "truncated")
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    content_type: str
    data: bytes
    truncated: bool
    def __init__(self, content_type: _Optional[str] = ..., data: _Optional[bytes] = ..., truncated: _Optional[bool] = ...) -> None: ...

class Observation(_message.Message):
    __slots__ = ("observation_id", "feed_id", "driver", "decoder", "source_entity_id", "source", "sensor_id", "source_timestamp", "received_at", "entity_type", "track_environment", "display_name", "identities", "position", "heading", "speed_knots", "course", "velocity", "acceleration", "orientation", "uncertainty", "position_covariance", "position_velocity_covariance", "velocity_covariance", "processing_state", "country_of_origin", "affiliation", "operational_status", "dimensions", "platform", "raw", "replica_id", "fencing_token", "classification")
    OBSERVATION_ID_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    DRIVER_FIELD_NUMBER: _ClassVar[int]
    DECODER_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    SENSOR_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    RECEIVED_AT_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    TRACK_ENVIRONMENT_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    IDENTITIES_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_FIELD_NUMBER: _ClassVar[int]
    ACCELERATION_FIELD_NUMBER: _ClassVar[int]
    ORIENTATION_FIELD_NUMBER: _ClassVar[int]
    UNCERTAINTY_FIELD_NUMBER: _ClassVar[int]
    POSITION_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    POSITION_VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    PROCESSING_STATE_FIELD_NUMBER: _ClassVar[int]
    COUNTRY_OF_ORIGIN_FIELD_NUMBER: _ClassVar[int]
    AFFILIATION_FIELD_NUMBER: _ClassVar[int]
    OPERATIONAL_STATUS_FIELD_NUMBER: _ClassVar[int]
    DIMENSIONS_FIELD_NUMBER: _ClassVar[int]
    PLATFORM_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    REPLICA_ID_FIELD_NUMBER: _ClassVar[int]
    FENCING_TOKEN_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    observation_id: str
    feed_id: str
    driver: str
    decoder: str
    source_entity_id: str
    source: _entity_pb2.EntitySource
    sensor_id: str
    source_timestamp: _timestamp_pb2.Timestamp
    received_at: _timestamp_pb2.Timestamp
    entity_type: _entity_pb2.EntityType
    track_environment: _common_pb2.TrackEnvironment
    display_name: str
    identities: _containers.RepeatedCompositeFieldContainer[_common_pb2.Identity]
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    velocity: _entity_pb2.GeodeticVelocity
    acceleration: _entity_pb2.GeodeticAcceleration
    orientation: _entity_pb2.Orientation
    uncertainty: _entity_pb2.MeasurementUncertainty
    position_covariance: _entity_pb2.PositionCovariance
    position_velocity_covariance: _entity_pb2.PositionVelocityCovariance
    velocity_covariance: _entity_pb2.VelocityCovariance
    processing_state: _common_pb2.TrackProcessingState
    country_of_origin: str
    affiliation: _entity_pb2.Affiliation
    operational_status: _common_pb2.OperationalStatus
    dimensions: _entity_pb2.Dimensions
    platform: _platform_pb2.PlatformDetail
    raw: RawPayload
    replica_id: str
    fencing_token: int
    classification: _common_pb2.Classification
    def __init__(self, observation_id: _Optional[str] = ..., feed_id: _Optional[str] = ..., driver: _Optional[str] = ..., decoder: _Optional[str] = ..., source_entity_id: _Optional[str] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ..., sensor_id: _Optional[str] = ..., source_timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., received_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., entity_type: _Optional[_Union[_entity_pb2.EntityType, str]] = ..., track_environment: _Optional[_Union[_common_pb2.TrackEnvironment, str]] = ..., display_name: _Optional[str] = ..., identities: _Optional[_Iterable[_Union[_common_pb2.Identity, _Mapping]]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., velocity: _Optional[_Union[_entity_pb2.GeodeticVelocity, _Mapping]] = ..., acceleration: _Optional[_Union[_entity_pb2.GeodeticAcceleration, _Mapping]] = ..., orientation: _Optional[_Union[_entity_pb2.Orientation, _Mapping]] = ..., uncertainty: _Optional[_Union[_entity_pb2.MeasurementUncertainty, _Mapping]] = ..., position_covariance: _Optional[_Union[_entity_pb2.PositionCovariance, _Mapping]] = ..., position_velocity_covariance: _Optional[_Union[_entity_pb2.PositionVelocityCovariance, _Mapping]] = ..., velocity_covariance: _Optional[_Union[_entity_pb2.VelocityCovariance, _Mapping]] = ..., processing_state: _Optional[_Union[_common_pb2.TrackProcessingState, str]] = ..., country_of_origin: _Optional[str] = ..., affiliation: _Optional[_Union[_entity_pb2.Affiliation, str]] = ..., operational_status: _Optional[_Union[_common_pb2.OperationalStatus, str]] = ..., dimensions: _Optional[_Union[_entity_pb2.Dimensions, _Mapping]] = ..., platform: _Optional[_Union[_platform_pb2.PlatformDetail, _Mapping]] = ..., raw: _Optional[_Union[RawPayload, _Mapping]] = ..., replica_id: _Optional[str] = ..., fencing_token: _Optional[int] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ...) -> None: ...
