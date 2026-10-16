import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import entity_pb2 as _entity_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TrackPoint(_message.Message):
    __slots__ = ("id", "entity_id", "position", "heading", "speed_knots", "course", "timestamp", "source", "altitude", "track_processing_state", "velocity", "acceleration", "position_covariance", "position_velocity_covariance", "velocity_covariance", "circular_error", "altitude_error", "sensor_id", "feed_id", "observation_id", "source_entity_id", "entity_type")
    ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_FIELD_NUMBER: _ClassVar[int]
    TRACK_PROCESSING_STATE_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_FIELD_NUMBER: _ClassVar[int]
    ACCELERATION_FIELD_NUMBER: _ClassVar[int]
    POSITION_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    POSITION_VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    VELOCITY_COVARIANCE_FIELD_NUMBER: _ClassVar[int]
    CIRCULAR_ERROR_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_ERROR_FIELD_NUMBER: _ClassVar[int]
    SENSOR_ID_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    OBSERVATION_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    id: str
    entity_id: str
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    timestamp: _timestamp_pb2.Timestamp
    source: _entity_pb2.EntitySource
    altitude: float
    track_processing_state: _common_pb2.TrackProcessingState
    velocity: _entity_pb2.GeodeticVelocity
    acceleration: _entity_pb2.GeodeticAcceleration
    position_covariance: _entity_pb2.PositionCovariance
    position_velocity_covariance: _entity_pb2.PositionVelocityCovariance
    velocity_covariance: _entity_pb2.VelocityCovariance
    circular_error: float
    altitude_error: float
    sensor_id: str
    feed_id: str
    observation_id: str
    source_entity_id: str
    entity_type: _entity_pb2.EntityType
    def __init__(self, id: _Optional[str] = ..., entity_id: _Optional[str] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ..., altitude: _Optional[float] = ..., track_processing_state: _Optional[_Union[_common_pb2.TrackProcessingState, str]] = ..., velocity: _Optional[_Union[_entity_pb2.GeodeticVelocity, _Mapping]] = ..., acceleration: _Optional[_Union[_entity_pb2.GeodeticAcceleration, _Mapping]] = ..., position_covariance: _Optional[_Union[_entity_pb2.PositionCovariance, _Mapping]] = ..., position_velocity_covariance: _Optional[_Union[_entity_pb2.PositionVelocityCovariance, _Mapping]] = ..., velocity_covariance: _Optional[_Union[_entity_pb2.VelocityCovariance, _Mapping]] = ..., circular_error: _Optional[float] = ..., altitude_error: _Optional[float] = ..., sensor_id: _Optional[str] = ..., feed_id: _Optional[str] = ..., observation_id: _Optional[str] = ..., source_entity_id: _Optional[str] = ..., entity_type: _Optional[_Union[_entity_pb2.EntityType, str]] = ...) -> None: ...

class TrackSegment(_message.Message):
    __slots__ = ("segment_id", "entity_id", "points", "start_time", "end_time")
    SEGMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    POINTS_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    END_TIME_FIELD_NUMBER: _ClassVar[int]
    segment_id: str
    entity_id: str
    points: _containers.RepeatedCompositeFieldContainer[TrackPoint]
    start_time: _timestamp_pb2.Timestamp
    end_time: _timestamp_pb2.Timestamp
    def __init__(self, segment_id: _Optional[str] = ..., entity_id: _Optional[str] = ..., points: _Optional[_Iterable[_Union[TrackPoint, _Mapping]]] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., end_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class RecordTrackPointRequest(_message.Message):
    __slots__ = ("entity_id", "position", "heading", "speed_knots", "course", "timestamp", "source")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    HEADING_FIELD_NUMBER: _ClassVar[int]
    SPEED_KNOTS_FIELD_NUMBER: _ClassVar[int]
    COURSE_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    position: _common_pb2.Coordinate
    heading: float
    speed_knots: float
    course: float
    timestamp: _timestamp_pb2.Timestamp
    source: _entity_pb2.EntitySource
    def __init__(self, entity_id: _Optional[str] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., heading: _Optional[float] = ..., speed_knots: _Optional[float] = ..., course: _Optional[float] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ...) -> None: ...

class RecordTrackPointResponse(_message.Message):
    __slots__ = ("point",)
    POINT_FIELD_NUMBER: _ClassVar[int]
    point: TrackPoint
    def __init__(self, point: _Optional[_Union[TrackPoint, _Mapping]] = ...) -> None: ...

class GetTrackHistoryRequest(_message.Message):
    __slots__ = ("entity_id", "start_time", "end_time", "max_points", "simplify")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    END_TIME_FIELD_NUMBER: _ClassVar[int]
    MAX_POINTS_FIELD_NUMBER: _ClassVar[int]
    SIMPLIFY_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    start_time: _timestamp_pb2.Timestamp
    end_time: _timestamp_pb2.Timestamp
    max_points: int
    simplify: bool
    def __init__(self, entity_id: _Optional[str] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., end_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., max_points: _Optional[int] = ..., simplify: _Optional[bool] = ...) -> None: ...

class GetTrackHistoryResponse(_message.Message):
    __slots__ = ("segments", "total_points")
    SEGMENTS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_POINTS_FIELD_NUMBER: _ClassVar[int]
    segments: _containers.RepeatedCompositeFieldContainer[TrackSegment]
    total_points: int
    def __init__(self, segments: _Optional[_Iterable[_Union[TrackSegment, _Mapping]]] = ..., total_points: _Optional[int] = ...) -> None: ...

class TrackReplayRequest(_message.Message):
    __slots__ = ("entity_id", "start_time", "end_time", "speed_multiplier")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    END_TIME_FIELD_NUMBER: _ClassVar[int]
    SPEED_MULTIPLIER_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    start_time: _timestamp_pb2.Timestamp
    end_time: _timestamp_pb2.Timestamp
    speed_multiplier: float
    def __init__(self, entity_id: _Optional[str] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., end_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., speed_multiplier: _Optional[float] = ...) -> None: ...

class TrackReplayEvent(_message.Message):
    __slots__ = ("entity_id", "point", "replay_timestamp")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    POINT_FIELD_NUMBER: _ClassVar[int]
    REPLAY_TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    point: TrackPoint
    replay_timestamp: _timestamp_pb2.Timestamp
    def __init__(self, entity_id: _Optional[str] = ..., point: _Optional[_Union[TrackPoint, _Mapping]] = ..., replay_timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
