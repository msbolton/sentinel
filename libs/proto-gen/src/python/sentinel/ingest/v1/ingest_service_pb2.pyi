from sentinel.v1 import common_pb2 as _common_pb2
from sentinel.v1 import emplacement_pb2 as _emplacement_pb2
from sentinel.v1 import observation_pb2 as _observation_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AckStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ACK_STATUS_UNSPECIFIED: _ClassVar[AckStatus]
    ACK_STATUS_OK: _ClassVar[AckStatus]
    ACK_STATUS_REJECTED: _ClassVar[AckStatus]
    ACK_STATUS_DEAD_LETTERED: _ClassVar[AckStatus]
ACK_STATUS_UNSPECIFIED: AckStatus
ACK_STATUS_OK: AckStatus
ACK_STATUS_REJECTED: AckStatus
ACK_STATUS_DEAD_LETTERED: AckStatus

class GetSessionRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetSessionResponse(_message.Message):
    __slots__ = ("feed_id", "emplacements", "classification_floor", "exercise", "sender_managed_emplacements", "max_message_bytes", "max_messages_per_sec")
    FEED_ID_FIELD_NUMBER: _ClassVar[int]
    EMPLACEMENTS_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FLOOR_FIELD_NUMBER: _ClassVar[int]
    EXERCISE_FIELD_NUMBER: _ClassVar[int]
    SENDER_MANAGED_EMPLACEMENTS_FIELD_NUMBER: _ClassVar[int]
    MAX_MESSAGE_BYTES_FIELD_NUMBER: _ClassVar[int]
    MAX_MESSAGES_PER_SEC_FIELD_NUMBER: _ClassVar[int]
    feed_id: str
    emplacements: _containers.RepeatedCompositeFieldContainer[_emplacement_pb2.Emplacement]
    classification_floor: _common_pb2.Classification
    exercise: bool
    sender_managed_emplacements: bool
    max_message_bytes: int
    max_messages_per_sec: int
    def __init__(self, feed_id: _Optional[str] = ..., emplacements: _Optional[_Iterable[_Union[_emplacement_pb2.Emplacement, _Mapping]]] = ..., classification_floor: _Optional[_Union[_common_pb2.Classification, str]] = ..., exercise: _Optional[bool] = ..., sender_managed_emplacements: _Optional[bool] = ..., max_message_bytes: _Optional[int] = ..., max_messages_per_sec: _Optional[int] = ...) -> None: ...

class ListEmplacementsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListEmplacementsResponse(_message.Message):
    __slots__ = ("emplacements",)
    EMPLACEMENTS_FIELD_NUMBER: _ClassVar[int]
    emplacements: _containers.RepeatedCompositeFieldContainer[_emplacement_pb2.Emplacement]
    def __init__(self, emplacements: _Optional[_Iterable[_Union[_emplacement_pb2.Emplacement, _Mapping]]] = ...) -> None: ...

class UpsertEmplacementRequest(_message.Message):
    __slots__ = ("emplacement",)
    EMPLACEMENT_FIELD_NUMBER: _ClassVar[int]
    emplacement: _emplacement_pb2.Emplacement
    def __init__(self, emplacement: _Optional[_Union[_emplacement_pb2.Emplacement, _Mapping]] = ...) -> None: ...

class UpsertEmplacementResponse(_message.Message):
    __slots__ = ("emplacement",)
    EMPLACEMENT_FIELD_NUMBER: _ClassVar[int]
    emplacement: _emplacement_pb2.Emplacement
    def __init__(self, emplacement: _Optional[_Union[_emplacement_pb2.Emplacement, _Mapping]] = ...) -> None: ...

class RemoveEmplacementRequest(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class RemoveEmplacementResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class Report(_message.Message):
    __slots__ = ("client_ref", "observation", "emplacement_state", "emplacement_id")
    CLIENT_REF_FIELD_NUMBER: _ClassVar[int]
    OBSERVATION_FIELD_NUMBER: _ClassVar[int]
    EMPLACEMENT_STATE_FIELD_NUMBER: _ClassVar[int]
    EMPLACEMENT_ID_FIELD_NUMBER: _ClassVar[int]
    client_ref: str
    observation: _observation_pb2.Observation
    emplacement_state: _emplacement_pb2.EmplacementState
    emplacement_id: str
    def __init__(self, client_ref: _Optional[str] = ..., observation: _Optional[_Union[_observation_pb2.Observation, _Mapping]] = ..., emplacement_state: _Optional[_Union[_emplacement_pb2.EmplacementState, _Mapping]] = ..., emplacement_id: _Optional[str] = ...) -> None: ...

class Ack(_message.Message):
    __slots__ = ("client_ref", "status", "reason", "emplacement_id")
    CLIENT_REF_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    EMPLACEMENT_ID_FIELD_NUMBER: _ClassVar[int]
    client_ref: str
    status: AckStatus
    reason: str
    emplacement_id: str
    def __init__(self, client_ref: _Optional[str] = ..., status: _Optional[_Union[AckStatus, str]] = ..., reason: _Optional[str] = ..., emplacement_id: _Optional[str] = ...) -> None: ...

class StreamReportsRequest(_message.Message):
    __slots__ = ("report",)
    REPORT_FIELD_NUMBER: _ClassVar[int]
    report: Report
    def __init__(self, report: _Optional[_Union[Report, _Mapping]] = ...) -> None: ...

class StreamReportsResponse(_message.Message):
    __slots__ = ("ack",)
    ACK_FIELD_NUMBER: _ClassVar[int]
    ack: Ack
    def __init__(self, ack: _Optional[_Union[Ack, _Mapping]] = ...) -> None: ...

class PublishRequest(_message.Message):
    __slots__ = ("reports",)
    REPORTS_FIELD_NUMBER: _ClassVar[int]
    reports: _containers.RepeatedCompositeFieldContainer[Report]
    def __init__(self, reports: _Optional[_Iterable[_Union[Report, _Mapping]]] = ...) -> None: ...

class PublishResponse(_message.Message):
    __slots__ = ("acks",)
    ACKS_FIELD_NUMBER: _ClassVar[int]
    acks: _containers.RepeatedCompositeFieldContainer[Ack]
    def __init__(self, acks: _Optional[_Iterable[_Union[Ack, _Mapping]]] = ...) -> None: ...
