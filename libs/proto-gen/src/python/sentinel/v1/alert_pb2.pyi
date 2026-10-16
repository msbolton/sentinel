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

class AlertSeverity(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ALERT_SEVERITY_UNSPECIFIED: _ClassVar[AlertSeverity]
    ALERT_SEVERITY_INFO: _ClassVar[AlertSeverity]
    ALERT_SEVERITY_LOW: _ClassVar[AlertSeverity]
    ALERT_SEVERITY_MEDIUM: _ClassVar[AlertSeverity]
    ALERT_SEVERITY_HIGH: _ClassVar[AlertSeverity]
    ALERT_SEVERITY_CRITICAL: _ClassVar[AlertSeverity]

class AlertType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ALERT_TYPE_UNSPECIFIED: _ClassVar[AlertType]
    ALERT_TYPE_GEOFENCE_BREACH: _ClassVar[AlertType]
    ALERT_TYPE_SPEED_ANOMALY: _ClassVar[AlertType]
    ALERT_TYPE_PATTERN_DEVIATION: _ClassVar[AlertType]
    ALERT_TYPE_PROXIMITY: _ClassVar[AlertType]
    ALERT_TYPE_COMMUNICATION_ANOMALY: _ClassVar[AlertType]
    ALERT_TYPE_NEW_ENTITY: _ClassVar[AlertType]
    ALERT_TYPE_ENTITY_CLASSIFICATION_CHANGE: _ClassVar[AlertType]
    ALERT_TYPE_GEOFENCE_ENTRY: _ClassVar[AlertType]
    ALERT_TYPE_GEOFENCE_EXIT: _ClassVar[AlertType]
    ALERT_TYPE_PATTERN_MATCH: _ClassVar[AlertType]
    ALERT_TYPE_CUSTOM: _ClassVar[AlertType]
    ALERT_TYPE_PROXIMITY_ALERT: _ClassVar[AlertType]
    ALERT_TYPE_ENTITY_LOST: _ClassVar[AlertType]
    ALERT_TYPE_PATTERN_DETECTED: _ClassVar[AlertType]
    ALERT_TYPE_LINK_CHANGE: _ClassVar[AlertType]
    ALERT_TYPE_CLASSIFICATION_CHANGE: _ClassVar[AlertType]
    ALERT_TYPE_SYSTEM: _ClassVar[AlertType]

class AlertEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ALERT_EVENT_TYPE_UNSPECIFIED: _ClassVar[AlertEventType]
    ALERT_EVENT_TYPE_TRIGGERED: _ClassVar[AlertEventType]
    ALERT_EVENT_TYPE_ACKNOWLEDGED: _ClassVar[AlertEventType]
    ALERT_EVENT_TYPE_RESOLVED: _ClassVar[AlertEventType]

class RuleType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RULE_TYPE_UNSPECIFIED: _ClassVar[RuleType]
    RULE_TYPE_GEOFENCE: _ClassVar[RuleType]
    RULE_TYPE_SPEED_THRESHOLD: _ClassVar[RuleType]
    RULE_TYPE_PROXIMITY: _ClassVar[RuleType]
    RULE_TYPE_PATTERN: _ClassVar[RuleType]
ALERT_SEVERITY_UNSPECIFIED: AlertSeverity
ALERT_SEVERITY_INFO: AlertSeverity
ALERT_SEVERITY_LOW: AlertSeverity
ALERT_SEVERITY_MEDIUM: AlertSeverity
ALERT_SEVERITY_HIGH: AlertSeverity
ALERT_SEVERITY_CRITICAL: AlertSeverity
ALERT_TYPE_UNSPECIFIED: AlertType
ALERT_TYPE_GEOFENCE_BREACH: AlertType
ALERT_TYPE_SPEED_ANOMALY: AlertType
ALERT_TYPE_PATTERN_DEVIATION: AlertType
ALERT_TYPE_PROXIMITY: AlertType
ALERT_TYPE_COMMUNICATION_ANOMALY: AlertType
ALERT_TYPE_NEW_ENTITY: AlertType
ALERT_TYPE_ENTITY_CLASSIFICATION_CHANGE: AlertType
ALERT_TYPE_GEOFENCE_ENTRY: AlertType
ALERT_TYPE_GEOFENCE_EXIT: AlertType
ALERT_TYPE_PATTERN_MATCH: AlertType
ALERT_TYPE_CUSTOM: AlertType
ALERT_TYPE_PROXIMITY_ALERT: AlertType
ALERT_TYPE_ENTITY_LOST: AlertType
ALERT_TYPE_PATTERN_DETECTED: AlertType
ALERT_TYPE_LINK_CHANGE: AlertType
ALERT_TYPE_CLASSIFICATION_CHANGE: AlertType
ALERT_TYPE_SYSTEM: AlertType
ALERT_EVENT_TYPE_UNSPECIFIED: AlertEventType
ALERT_EVENT_TYPE_TRIGGERED: AlertEventType
ALERT_EVENT_TYPE_ACKNOWLEDGED: AlertEventType
ALERT_EVENT_TYPE_RESOLVED: AlertEventType
RULE_TYPE_UNSPECIFIED: RuleType
RULE_TYPE_GEOFENCE: RuleType
RULE_TYPE_SPEED_THRESHOLD: RuleType
RULE_TYPE_PROXIMITY: RuleType
RULE_TYPE_PATTERN: RuleType

class GeofenceRule(_message.Message):
    __slots__ = ("rule_id", "name", "polygon", "monitored_entity_types", "trigger_on_enter", "trigger_on_exit")
    RULE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    POLYGON_FIELD_NUMBER: _ClassVar[int]
    MONITORED_ENTITY_TYPES_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_ON_ENTER_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_ON_EXIT_FIELD_NUMBER: _ClassVar[int]
    rule_id: str
    name: str
    polygon: _containers.RepeatedCompositeFieldContainer[_common_pb2.Coordinate]
    monitored_entity_types: _containers.RepeatedScalarFieldContainer[_entity_pb2.EntityType]
    trigger_on_enter: bool
    trigger_on_exit: bool
    def __init__(self, rule_id: _Optional[str] = ..., name: _Optional[str] = ..., polygon: _Optional[_Iterable[_Union[_common_pb2.Coordinate, _Mapping]]] = ..., monitored_entity_types: _Optional[_Iterable[_Union[_entity_pb2.EntityType, str]]] = ..., trigger_on_enter: _Optional[bool] = ..., trigger_on_exit: _Optional[bool] = ...) -> None: ...

class Alert(_message.Message):
    __slots__ = ("alert_id", "alert_type", "severity", "title", "description", "entity_id", "related_entity_ids", "position", "rule_id", "created_at", "acknowledged_at", "acknowledged_by", "metadata")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ALERT_ID_FIELD_NUMBER: _ClassVar[int]
    ALERT_TYPE_FIELD_NUMBER: _ClassVar[int]
    SEVERITY_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    RELATED_ENTITY_IDS_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    RULE_ID_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    ACKNOWLEDGED_AT_FIELD_NUMBER: _ClassVar[int]
    ACKNOWLEDGED_BY_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    alert_id: str
    alert_type: AlertType
    severity: AlertSeverity
    title: str
    description: str
    entity_id: str
    related_entity_ids: _containers.RepeatedScalarFieldContainer[str]
    position: _common_pb2.Coordinate
    rule_id: str
    created_at: _timestamp_pb2.Timestamp
    acknowledged_at: _timestamp_pb2.Timestamp
    acknowledged_by: str
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, alert_id: _Optional[str] = ..., alert_type: _Optional[_Union[AlertType, str]] = ..., severity: _Optional[_Union[AlertSeverity, str]] = ..., title: _Optional[str] = ..., description: _Optional[str] = ..., entity_id: _Optional[str] = ..., related_entity_ids: _Optional[_Iterable[str]] = ..., position: _Optional[_Union[_common_pb2.Coordinate, _Mapping]] = ..., rule_id: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., acknowledged_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., acknowledged_by: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class CreateAlertRuleRequest(_message.Message):
    __slots__ = ("name", "polygon", "monitored_entity_types", "trigger_on_enter", "trigger_on_exit")
    NAME_FIELD_NUMBER: _ClassVar[int]
    POLYGON_FIELD_NUMBER: _ClassVar[int]
    MONITORED_ENTITY_TYPES_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_ON_ENTER_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_ON_EXIT_FIELD_NUMBER: _ClassVar[int]
    name: str
    polygon: _containers.RepeatedCompositeFieldContainer[_common_pb2.Coordinate]
    monitored_entity_types: _containers.RepeatedScalarFieldContainer[_entity_pb2.EntityType]
    trigger_on_enter: bool
    trigger_on_exit: bool
    def __init__(self, name: _Optional[str] = ..., polygon: _Optional[_Iterable[_Union[_common_pb2.Coordinate, _Mapping]]] = ..., monitored_entity_types: _Optional[_Iterable[_Union[_entity_pb2.EntityType, str]]] = ..., trigger_on_enter: _Optional[bool] = ..., trigger_on_exit: _Optional[bool] = ...) -> None: ...

class CreateAlertRuleResponse(_message.Message):
    __slots__ = ("rule",)
    RULE_FIELD_NUMBER: _ClassVar[int]
    rule: GeofenceRule
    def __init__(self, rule: _Optional[_Union[GeofenceRule, _Mapping]] = ...) -> None: ...

class GetAlertsRequest(_message.Message):
    __slots__ = ("severity_min", "alert_types", "entity_id", "bounding_box", "start_time", "end_time", "acknowledged", "pagination")
    SEVERITY_MIN_FIELD_NUMBER: _ClassVar[int]
    ALERT_TYPES_FIELD_NUMBER: _ClassVar[int]
    ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    BOUNDING_BOX_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    END_TIME_FIELD_NUMBER: _ClassVar[int]
    ACKNOWLEDGED_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    severity_min: AlertSeverity
    alert_types: _containers.RepeatedScalarFieldContainer[AlertType]
    entity_id: str
    bounding_box: _common_pb2.BoundingBox
    start_time: _timestamp_pb2.Timestamp
    end_time: _timestamp_pb2.Timestamp
    acknowledged: bool
    pagination: _common_pb2.Pagination
    def __init__(self, severity_min: _Optional[_Union[AlertSeverity, str]] = ..., alert_types: _Optional[_Iterable[_Union[AlertType, str]]] = ..., entity_id: _Optional[str] = ..., bounding_box: _Optional[_Union[_common_pb2.BoundingBox, _Mapping]] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., end_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., acknowledged: _Optional[bool] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...

class GetAlertsResponse(_message.Message):
    __slots__ = ("alerts", "pagination")
    ALERTS_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    alerts: _containers.RepeatedCompositeFieldContainer[Alert]
    pagination: _common_pb2.Pagination
    def __init__(self, alerts: _Optional[_Iterable[_Union[Alert, _Mapping]]] = ..., pagination: _Optional[_Union[_common_pb2.Pagination, _Mapping]] = ...) -> None: ...

class AcknowledgeAlertRequest(_message.Message):
    __slots__ = ("alert_id", "acknowledged_by")
    ALERT_ID_FIELD_NUMBER: _ClassVar[int]
    ACKNOWLEDGED_BY_FIELD_NUMBER: _ClassVar[int]
    alert_id: str
    acknowledged_by: str
    def __init__(self, alert_id: _Optional[str] = ..., acknowledged_by: _Optional[str] = ...) -> None: ...

class AcknowledgeAlertResponse(_message.Message):
    __slots__ = ("alert",)
    ALERT_FIELD_NUMBER: _ClassVar[int]
    alert: Alert
    def __init__(self, alert: _Optional[_Union[Alert, _Mapping]] = ...) -> None: ...

class AlertEvent(_message.Message):
    __slots__ = ("alert", "event_type", "producer", "anomaly_type", "score", "expected_value", "actual_value", "event_time")
    ALERT_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    PRODUCER_FIELD_NUMBER: _ClassVar[int]
    ANOMALY_TYPE_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    EXPECTED_VALUE_FIELD_NUMBER: _ClassVar[int]
    ACTUAL_VALUE_FIELD_NUMBER: _ClassVar[int]
    EVENT_TIME_FIELD_NUMBER: _ClassVar[int]
    alert: Alert
    event_type: AlertEventType
    producer: str
    anomaly_type: str
    score: float
    expected_value: float
    actual_value: float
    event_time: _timestamp_pb2.Timestamp
    def __init__(self, alert: _Optional[_Union[Alert, _Mapping]] = ..., event_type: _Optional[_Union[AlertEventType, str]] = ..., producer: _Optional[str] = ..., anomaly_type: _Optional[str] = ..., score: _Optional[float] = ..., expected_value: _Optional[float] = ..., actual_value: _Optional[float] = ..., event_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
