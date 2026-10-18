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

class ObservationRef(_message.Message):
    __slots__ = ("observation_id", "feed_id", "source", "source_entity_id", "source_timestamp", "background")
    OBSERVATION_ID_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    BACKGROUND_FIELD_NUMBER: _ClassVar[int]
    observation_id: str
    feed_id: str
    source: _entity_pb2.EntitySource
    source_entity_id: str
    source_timestamp: _timestamp_pb2.Timestamp
    background: bool
    def __init__(self, observation_id: _Optional[str] = ..., feed_id: _Optional[str] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ..., source_entity_id: _Optional[str] = ..., source_timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., background: _Optional[bool] = ...) -> None: ...

class EntityCreated(_message.Message):
    __slots__ = ("entity", "observation")
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    OBSERVATION_FIELD_NUMBER: _ClassVar[int]
    entity: _entity_pb2.Entity
    observation: ObservationRef
    def __init__(self, entity: _Optional[_Union[_entity_pb2.Entity, _Mapping]] = ..., observation: _Optional[_Union[ObservationRef, _Mapping]] = ...) -> None: ...

class EntityUpdated(_message.Message):
    __slots__ = ("entity", "changed_fields")
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    CHANGED_FIELDS_FIELD_NUMBER: _ClassVar[int]
    entity: _entity_pb2.Entity
    changed_fields: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, entity: _Optional[_Union[_entity_pb2.Entity, _Mapping]] = ..., changed_fields: _Optional[_Iterable[str]] = ...) -> None: ...

class EntityPosition(_message.Message):
    __slots__ = ("entity", "observation")
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    OBSERVATION_FIELD_NUMBER: _ClassVar[int]
    entity: _entity_pb2.Entity
    observation: ObservationRef
    def __init__(self, entity: _Optional[_Union[_entity_pb2.Entity, _Mapping]] = ..., observation: _Optional[_Union[ObservationRef, _Mapping]] = ...) -> None: ...

class EntityDeleted(_message.Message):
    __slots__ = ("bulk", "count")
    BULK_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    bulk: bool
    count: int
    def __init__(self, bulk: _Optional[bool] = ..., count: _Optional[int] = ...) -> None: ...

class EntityAgeout(_message.Message):
    __slots__ = ("entity_type", "source", "feed_id", "previous_state", "last_seen_at", "threshold_ms")
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_STATE_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_AT_FIELD_NUMBER: _ClassVar[int]
    THRESHOLD_MS_FIELD_NUMBER: _ClassVar[int]
    entity_type: _entity_pb2.EntityType
    source: _entity_pb2.EntitySource
    feed_id: str
    previous_state: _common_pb2.AgeoutState
    last_seen_at: _timestamp_pb2.Timestamp
    threshold_ms: int
    def __init__(self, entity_type: _Optional[_Union[_entity_pb2.EntityType, str]] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ..., feed_id: _Optional[str] = ..., previous_state: _Optional[_Union[_common_pb2.AgeoutState, str]] = ..., last_seen_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., threshold_ms: _Optional[int] = ...) -> None: ...

class EntityRestored(_message.Message):
    __slots__ = ("entity", "previous_state")
    ENTITY_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_STATE_FIELD_NUMBER: _ClassVar[int]
    entity: _entity_pb2.Entity
    previous_state: _common_pb2.AgeoutState
    def __init__(self, entity: _Optional[_Union[_entity_pb2.Entity, _Mapping]] = ..., previous_state: _Optional[_Union[_common_pb2.AgeoutState, str]] = ...) -> None: ...

class EntityEvent(_message.Message):
    __slots__ = ("event_id", "event_time", "entity_id", "producer", "created", "updated", "position", "deleted", "stale", "aged_out", "restored")
    EVENT_ID_FIELD_NUMBER: _ClassVar[int]
    EVENT_TIME_FIELD_NUMBER: _ClassVar[int]
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    PRODUCER_FIELD_NUMBER: _ClassVar[int]
    CREATED_FIELD_NUMBER: _ClassVar[int]
    UPDATED_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    DELETED_FIELD_NUMBER: _ClassVar[int]
    STALE_FIELD_NUMBER: _ClassVar[int]
    AGED_OUT_FIELD_NUMBER: _ClassVar[int]
    RESTORED_FIELD_NUMBER: _ClassVar[int]
    event_id: str
    event_time: _timestamp_pb2.Timestamp
    entity_id: str
    producer: str
    created: EntityCreated
    updated: EntityUpdated
    position: EntityPosition
    deleted: EntityDeleted
    stale: EntityAgeout
    aged_out: EntityAgeout
    restored: EntityRestored
    def __init__(self, event_id: _Optional[str] = ..., event_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., entity_id: _Optional[str] = ..., producer: _Optional[str] = ..., created: _Optional[_Union[EntityCreated, _Mapping]] = ..., updated: _Optional[_Union[EntityUpdated, _Mapping]] = ..., position: _Optional[_Union[EntityPosition, _Mapping]] = ..., deleted: _Optional[_Union[EntityDeleted, _Mapping]] = ..., stale: _Optional[_Union[EntityAgeout, _Mapping]] = ..., aged_out: _Optional[_Union[EntityAgeout, _Mapping]] = ..., restored: _Optional[_Union[EntityRestored, _Mapping]] = ...) -> None: ...

class DensityCell(_message.Message):
    __slots__ = ("latitude", "longitude", "count")
    LATITUDE_FIELD_NUMBER: _ClassVar[int]
    LONGITUDE_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    latitude: float
    longitude: float
    count: int
    def __init__(self, latitude: _Optional[float] = ..., longitude: _Optional[float] = ..., count: _Optional[int] = ...) -> None: ...

class DensityGrid(_message.Message):
    __slots__ = ("feed_id", "source", "entity_type", "cell_degrees", "window_seconds", "cap_square_degrees", "generated_at", "classification", "cells", "total")
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    CELL_DEGREES_FIELD_NUMBER: _ClassVar[int]
    WINDOW_SECONDS_FIELD_NUMBER: _ClassVar[int]
    CAP_SQUARE_DEGREES_FIELD_NUMBER: _ClassVar[int]
    GENERATED_AT_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    CELLS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    feed_id: str
    source: _entity_pb2.EntitySource
    entity_type: _entity_pb2.EntityType
    cell_degrees: float
    window_seconds: int
    cap_square_degrees: float
    generated_at: _timestamp_pb2.Timestamp
    classification: _common_pb2.Classification
    cells: _containers.RepeatedCompositeFieldContainer[DensityCell]
    total: int
    def __init__(self, feed_id: _Optional[str] = ..., source: _Optional[_Union[_entity_pb2.EntitySource, str]] = ..., entity_type: _Optional[_Union[_entity_pb2.EntityType, str]] = ..., cell_degrees: _Optional[float] = ..., window_seconds: _Optional[int] = ..., cap_square_degrees: _Optional[float] = ..., generated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., classification: _Optional[_Union[_common_pb2.Classification, str]] = ..., cells: _Optional[_Iterable[_Union[DensityCell, _Mapping]]] = ..., total: _Optional[int] = ...) -> None: ...
