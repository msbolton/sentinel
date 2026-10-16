import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import entity_pb2 as _entity_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class LinkType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LINK_TYPE_UNSPECIFIED: _ClassVar[LinkType]
    LINK_TYPE_COMMUNICATION: _ClassVar[LinkType]
    LINK_TYPE_ASSOCIATION: _ClassVar[LinkType]
    LINK_TYPE_COLOCATION: _ClassVar[LinkType]
    LINK_TYPE_FINANCIAL: _ClassVar[LinkType]
    LINK_TYPE_ORGANIZATIONAL: _ClassVar[LinkType]
    LINK_TYPE_MOVEMENT_PATTERN: _ClassVar[LinkType]
    LINK_TYPE_COMMAND_CONTROL: _ClassVar[LinkType]
    LINK_TYPE_GEOGRAPHIC: _ClassVar[LinkType]
    LINK_TYPE_FAMILIAL: _ClassVar[LinkType]
    LINK_TYPE_LOGISTIC: _ClassVar[LinkType]
    LINK_TYPE_OPERATIONAL: _ClassVar[LinkType]
    LINK_TYPE_IDENTITY: _ClassVar[LinkType]

class LinkEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LINK_EVENT_TYPE_UNSPECIFIED: _ClassVar[LinkEventType]
    LINK_EVENT_TYPE_CREATED: _ClassVar[LinkEventType]
    LINK_EVENT_TYPE_UPDATED: _ClassVar[LinkEventType]
    LINK_EVENT_TYPE_DELETED: _ClassVar[LinkEventType]
LINK_TYPE_UNSPECIFIED: LinkType
LINK_TYPE_COMMUNICATION: LinkType
LINK_TYPE_ASSOCIATION: LinkType
LINK_TYPE_COLOCATION: LinkType
LINK_TYPE_FINANCIAL: LinkType
LINK_TYPE_ORGANIZATIONAL: LinkType
LINK_TYPE_MOVEMENT_PATTERN: LinkType
LINK_TYPE_COMMAND_CONTROL: LinkType
LINK_TYPE_GEOGRAPHIC: LinkType
LINK_TYPE_FAMILIAL: LinkType
LINK_TYPE_LOGISTIC: LinkType
LINK_TYPE_OPERATIONAL: LinkType
LINK_TYPE_IDENTITY: LinkType
LINK_EVENT_TYPE_UNSPECIFIED: LinkEventType
LINK_EVENT_TYPE_CREATED: LinkEventType
LINK_EVENT_TYPE_UPDATED: LinkEventType
LINK_EVENT_TYPE_DELETED: LinkEventType

class Link(_message.Message):
    __slots__ = ("link_id", "source_entity_id", "target_entity_id", "link_type", "confidence", "description", "evidence", "first_observed", "last_observed", "metadata")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    LINK_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    TARGET_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    LINK_TYPE_FIELD_NUMBER: _ClassVar[int]
    CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    EVIDENCE_FIELD_NUMBER: _ClassVar[int]
    FIRST_OBSERVED_FIELD_NUMBER: _ClassVar[int]
    LAST_OBSERVED_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    link_id: str
    source_entity_id: str
    target_entity_id: str
    link_type: LinkType
    confidence: float
    description: str
    evidence: _containers.RepeatedScalarFieldContainer[str]
    first_observed: _timestamp_pb2.Timestamp
    last_observed: _timestamp_pb2.Timestamp
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, link_id: _Optional[str] = ..., source_entity_id: _Optional[str] = ..., target_entity_id: _Optional[str] = ..., link_type: _Optional[_Union[LinkType, str]] = ..., confidence: _Optional[float] = ..., description: _Optional[str] = ..., evidence: _Optional[_Iterable[str]] = ..., first_observed: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_observed: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class LinkEvent(_message.Message):
    __slots__ = ("link", "event_type")
    LINK_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    link: Link
    event_type: LinkEventType
    def __init__(self, link: _Optional[_Union[Link, _Mapping]] = ..., event_type: _Optional[_Union[LinkEventType, str]] = ...) -> None: ...

class GetLinksRequest(_message.Message):
    __slots__ = ("entity_id", "link_types", "min_confidence", "max_hops", "pagination")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    LINK_TYPES_FIELD_NUMBER: _ClassVar[int]
    MIN_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    MAX_HOPS_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    link_types: _containers.RepeatedScalarFieldContainer[LinkType]
    min_confidence: float
    max_hops: int
    pagination: _common_pb2.Pagination
    def __init__(self, entity_id: _Optional[str] = ..., link_types: _Optional[_Iterable[_Union[LinkType, str]]] = ..., min_confidence: _Optional[float] = ..., max_hops: _Optional[int] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...

class GetLinksResponse(_message.Message):
    __slots__ = ("links", "pagination")
    LINKS_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    links: _containers.RepeatedCompositeFieldContainer[Link]
    pagination: _common_pb2.Pagination
    def __init__(self, links: _Optional[_Iterable[_Union[Link, _Mapping]]] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...

class GetGraphRequest(_message.Message):
    __slots__ = ("center_entity_id", "max_depth", "link_types", "min_confidence")
    CENTER_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    MAX_DEPTH_FIELD_NUMBER: _ClassVar[int]
    LINK_TYPES_FIELD_NUMBER: _ClassVar[int]
    MIN_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    center_entity_id: str
    max_depth: int
    link_types: _containers.RepeatedScalarFieldContainer[LinkType]
    min_confidence: float
    def __init__(self, center_entity_id: _Optional[str] = ..., max_depth: _Optional[int] = ..., link_types: _Optional[_Iterable[_Union[LinkType, str]]] = ..., min_confidence: _Optional[float] = ...) -> None: ...

class GraphNode(_message.Message):
    __slots__ = ("entity_id", "entity_type", "name", "position")
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    ENTITY_TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    entity_id: str
    entity_type: _entity_pb2.EntityType
    name: str
    position: _common_pb2.Coordinate
    def __init__(self, entity_id: _Optional[str] = ..., entity_type: _Optional[_Union[_entity_pb2.EntityType, str]] = ..., name: _Optional[str] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ...) -> None: ...

class GraphEdge(_message.Message):
    __slots__ = ("link",)
    LINK_FIELD_NUMBER: _ClassVar[int]
    link: Link
    def __init__(self, link: _Optional[_Union[Link, _Mapping]] = ...) -> None: ...

class GetGraphResponse(_message.Message):
    __slots__ = ("nodes", "edges")
    NODES_FIELD_NUMBER: _ClassVar[int]
    EDGES_FIELD_NUMBER: _ClassVar[int]
    nodes: _containers.RepeatedCompositeFieldContainer[GraphNode]
    edges: _containers.RepeatedCompositeFieldContainer[GraphEdge]
    def __init__(self, nodes: _Optional[_Iterable[_Union[GraphNode, _Mapping]]] = ..., edges: _Optional[_Iterable[_Union[GraphEdge, _Mapping]]] = ...) -> None: ...
