from sentinel.v1 import common_pb2 as _common_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RadarMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RADAR_MODE_UNSPECIFIED: _ClassVar[RadarMode]
    RADAR_MODE_SEARCH: _ClassVar[RadarMode]
    RADAR_MODE_TRACK: _ClassVar[RadarMode]

class RadarClass(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RADAR_CLASS_UNSPECIFIED: _ClassVar[RadarClass]
    RADAR_CLASS_VEHICLE: _ClassVar[RadarClass]
    RADAR_CLASS_DRONE: _ClassVar[RadarClass]
    RADAR_CLASS_PERSON: _ClassVar[RadarClass]
    RADAR_CLASS_BIRD: _ClassVar[RadarClass]
    RADAR_CLASS_CLUTTER: _ClassVar[RadarClass]
    RADAR_CLASS_UNKNOWN: _ClassVar[RadarClass]

class EoIrBand(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EO_IR_BAND_UNSPECIFIED: _ClassVar[EoIrBand]
    EO_IR_BAND_VISIBLE: _ClassVar[EoIrBand]
    EO_IR_BAND_SWIR: _ClassVar[EoIrBand]
    EO_IR_BAND_MWIR: _ClassVar[EoIrBand]
    EO_IR_BAND_LWIR: _ClassVar[EoIrBand]
RADAR_MODE_UNSPECIFIED: RadarMode
RADAR_MODE_SEARCH: RadarMode
RADAR_MODE_TRACK: RadarMode
RADAR_CLASS_UNSPECIFIED: RadarClass
RADAR_CLASS_VEHICLE: RadarClass
RADAR_CLASS_DRONE: RadarClass
RADAR_CLASS_PERSON: RadarClass
RADAR_CLASS_BIRD: RadarClass
RADAR_CLASS_CLUTTER: RadarClass
RADAR_CLASS_UNKNOWN: RadarClass
EO_IR_BAND_UNSPECIFIED: EoIrBand
EO_IR_BAND_VISIBLE: EoIrBand
EO_IR_BAND_SWIR: EoIrBand
EO_IR_BAND_MWIR: EoIrBand
EO_IR_BAND_LWIR: EoIrBand

class AisDetail(_message.Message):
    __slots__ = ("mmsi", "imo", "callsign", "vessel_name", "ship_type", "ship_type_name", "flag", "destination", "eta", "draught", "dimension_a", "dimension_b", "dimension_c", "dimension_d", "length_overall", "beam", "nav_status", "rate_of_turn", "speed_over_ground", "course_over_ground", "true_heading", "position_accuracy_high", "special_manoeuvre", "message_type", "repeat_indicator")
    MMSI_FIELD_NUMBER: _ClassVar[int]
    IMO_FIELD_NUMBER: _ClassVar[int]
    CALLSIGN_FIELD_NUMBER: _ClassVar[int]
    VESSEL_NAME_FIELD_NUMBER: _ClassVar[int]
    SHIP_TYPE_FIELD_NUMBER: _ClassVar[int]
    SHIP_TYPE_NAME_FIELD_NUMBER: _ClassVar[int]
    FLAG_FIELD_NUMBER: _ClassVar[int]
    DESTINATION_FIELD_NUMBER: _ClassVar[int]
    ETA_FIELD_NUMBER: _ClassVar[int]
    DRAUGHT_FIELD_NUMBER: _ClassVar[int]
    DIMENSION_A_FIELD_NUMBER: _ClassVar[int]
    DIMENSION_B_FIELD_NUMBER: _ClassVar[int]
    DIMENSION_C_FIELD_NUMBER: _ClassVar[int]
    DIMENSION_D_FIELD_NUMBER: _ClassVar[int]
    LENGTH_OVERALL_FIELD_NUMBER: _ClassVar[int]
    BEAM_FIELD_NUMBER: _ClassVar[int]
    NAV_STATUS_FIELD_NUMBER: _ClassVar[int]
    RATE_OF_TURN_FIELD_NUMBER: _ClassVar[int]
    SPEED_OVER_GROUND_FIELD_NUMBER: _ClassVar[int]
    COURSE_OVER_GROUND_FIELD_NUMBER: _ClassVar[int]
    TRUE_HEADING_FIELD_NUMBER: _ClassVar[int]
    POSITION_ACCURACY_HIGH_FIELD_NUMBER: _ClassVar[int]
    SPECIAL_MANOEUVRE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_TYPE_FIELD_NUMBER: _ClassVar[int]
    REPEAT_INDICATOR_FIELD_NUMBER: _ClassVar[int]
    mmsi: str
    imo: str
    callsign: str
    vessel_name: str
    ship_type: int
    ship_type_name: str
    flag: str
    destination: str
    eta: str
    draught: float
    dimension_a: float
    dimension_b: float
    dimension_c: float
    dimension_d: float
    length_overall: float
    beam: float
    nav_status: _common_pb2.NavigationalStatus
    rate_of_turn: float
    speed_over_ground: float
    course_over_ground: float
    true_heading: float
    position_accuracy_high: bool
    special_manoeuvre: bool
    message_type: int
    repeat_indicator: int
    def __init__(self, mmsi: _Optional[str] = ..., imo: _Optional[str] = ..., callsign: _Optional[str] = ..., vessel_name: _Optional[str] = ..., ship_type: _Optional[int] = ..., ship_type_name: _Optional[str] = ..., flag: _Optional[str] = ..., destination: _Optional[str] = ..., eta: _Optional[str] = ..., draught: _Optional[float] = ..., dimension_a: _Optional[float] = ..., dimension_b: _Optional[float] = ..., dimension_c: _Optional[float] = ..., dimension_d: _Optional[float] = ..., length_overall: _Optional[float] = ..., beam: _Optional[float] = ..., nav_status: _Optional[_Union[_common_pb2.NavigationalStatus, str]] = ..., rate_of_turn: _Optional[float] = ..., speed_over_ground: _Optional[float] = ..., course_over_ground: _Optional[float] = ..., true_heading: _Optional[float] = ..., position_accuracy_high: _Optional[bool] = ..., special_manoeuvre: _Optional[bool] = ..., message_type: _Optional[int] = ..., repeat_indicator: _Optional[int] = ...) -> None: ...

class AdsbDetail(_message.Message):
    __slots__ = ("icao_hex", "registration", "aircraft_type", "aircraft_type_name", "operator_icao", "operator_name", "squawk", "emergency", "mode1", "mode2", "aircraft_id", "flight_airborne", "indicated_air_speed", "true_air_speed", "ground_speed", "magnetic_heading", "mode5_figure_of_merit", "national_origin_code", "mission_code", "altitude_baro", "altitude_geom", "vertical_rate", "on_ground", "category", "nac_p", "nac_v", "sil", "sil_type", "nic", "rc", "gva", "sda")
    ICAO_HEX_FIELD_NUMBER: _ClassVar[int]
    REGISTRATION_FIELD_NUMBER: _ClassVar[int]
    AIRCRAFT_TYPE_FIELD_NUMBER: _ClassVar[int]
    AIRCRAFT_TYPE_NAME_FIELD_NUMBER: _ClassVar[int]
    OPERATOR_ICAO_FIELD_NUMBER: _ClassVar[int]
    OPERATOR_NAME_FIELD_NUMBER: _ClassVar[int]
    SQUAWK_FIELD_NUMBER: _ClassVar[int]
    EMERGENCY_FIELD_NUMBER: _ClassVar[int]
    MODE1_FIELD_NUMBER: _ClassVar[int]
    MODE2_FIELD_NUMBER: _ClassVar[int]
    AIRCRAFT_ID_FIELD_NUMBER: _ClassVar[int]
    FLIGHT_AIRBORNE_FIELD_NUMBER: _ClassVar[int]
    INDICATED_AIR_SPEED_FIELD_NUMBER: _ClassVar[int]
    TRUE_AIR_SPEED_FIELD_NUMBER: _ClassVar[int]
    GROUND_SPEED_FIELD_NUMBER: _ClassVar[int]
    MAGNETIC_HEADING_FIELD_NUMBER: _ClassVar[int]
    MODE5_FIGURE_OF_MERIT_FIELD_NUMBER: _ClassVar[int]
    NATIONAL_ORIGIN_CODE_FIELD_NUMBER: _ClassVar[int]
    MISSION_CODE_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_BARO_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_GEOM_FIELD_NUMBER: _ClassVar[int]
    VERTICAL_RATE_FIELD_NUMBER: _ClassVar[int]
    ON_GROUND_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    NAC_P_FIELD_NUMBER: _ClassVar[int]
    NAC_V_FIELD_NUMBER: _ClassVar[int]
    SIL_FIELD_NUMBER: _ClassVar[int]
    SIL_TYPE_FIELD_NUMBER: _ClassVar[int]
    NIC_FIELD_NUMBER: _ClassVar[int]
    RC_FIELD_NUMBER: _ClassVar[int]
    GVA_FIELD_NUMBER: _ClassVar[int]
    SDA_FIELD_NUMBER: _ClassVar[int]
    icao_hex: str
    registration: str
    aircraft_type: str
    aircraft_type_name: str
    operator_icao: str
    operator_name: str
    squawk: str
    emergency: str
    mode1: str
    mode2: str
    aircraft_id: str
    flight_airborne: bool
    indicated_air_speed: float
    true_air_speed: float
    ground_speed: float
    magnetic_heading: float
    mode5_figure_of_merit: int
    national_origin_code: int
    mission_code: int
    altitude_baro: float
    altitude_geom: float
    vertical_rate: float
    on_ground: bool
    category: str
    nac_p: int
    nac_v: int
    sil: int
    sil_type: str
    nic: int
    rc: float
    gva: int
    sda: int
    def __init__(self, icao_hex: _Optional[str] = ..., registration: _Optional[str] = ..., aircraft_type: _Optional[str] = ..., aircraft_type_name: _Optional[str] = ..., operator_icao: _Optional[str] = ..., operator_name: _Optional[str] = ..., squawk: _Optional[str] = ..., emergency: _Optional[str] = ..., mode1: _Optional[str] = ..., mode2: _Optional[str] = ..., aircraft_id: _Optional[str] = ..., flight_airborne: _Optional[bool] = ..., indicated_air_speed: _Optional[float] = ..., true_air_speed: _Optional[float] = ..., ground_speed: _Optional[float] = ..., magnetic_heading: _Optional[float] = ..., mode5_figure_of_merit: _Optional[int] = ..., national_origin_code: _Optional[int] = ..., mission_code: _Optional[int] = ..., altitude_baro: _Optional[float] = ..., altitude_geom: _Optional[float] = ..., vertical_rate: _Optional[float] = ..., on_ground: _Optional[bool] = ..., category: _Optional[str] = ..., nac_p: _Optional[int] = ..., nac_v: _Optional[int] = ..., sil: _Optional[int] = ..., sil_type: _Optional[str] = ..., nic: _Optional[int] = ..., rc: _Optional[float] = ..., gva: _Optional[int] = ..., sda: _Optional[int] = ...) -> None: ...

class TleDetail(_message.Message):
    __slots__ = ("norad_id", "intl_designator", "sat_name", "line1", "line2", "epoch", "inclination", "eccentricity", "raan", "arg_of_perigee", "mean_anomaly", "mean_motion", "period", "apogee", "perigee", "object_type", "rcs_size", "launch_date", "decay_date", "country")
    NORAD_ID_FIELD_NUMBER: _ClassVar[int]
    INTL_DESIGNATOR_FIELD_NUMBER: _ClassVar[int]
    SAT_NAME_FIELD_NUMBER: _ClassVar[int]
    LINE1_FIELD_NUMBER: _ClassVar[int]
    LINE2_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    INCLINATION_FIELD_NUMBER: _ClassVar[int]
    ECCENTRICITY_FIELD_NUMBER: _ClassVar[int]
    RAAN_FIELD_NUMBER: _ClassVar[int]
    ARG_OF_PERIGEE_FIELD_NUMBER: _ClassVar[int]
    MEAN_ANOMALY_FIELD_NUMBER: _ClassVar[int]
    MEAN_MOTION_FIELD_NUMBER: _ClassVar[int]
    PERIOD_FIELD_NUMBER: _ClassVar[int]
    APOGEE_FIELD_NUMBER: _ClassVar[int]
    PERIGEE_FIELD_NUMBER: _ClassVar[int]
    OBJECT_TYPE_FIELD_NUMBER: _ClassVar[int]
    RCS_SIZE_FIELD_NUMBER: _ClassVar[int]
    LAUNCH_DATE_FIELD_NUMBER: _ClassVar[int]
    DECAY_DATE_FIELD_NUMBER: _ClassVar[int]
    COUNTRY_FIELD_NUMBER: _ClassVar[int]
    norad_id: int
    intl_designator: str
    sat_name: str
    line1: str
    line2: str
    epoch: str
    inclination: float
    eccentricity: float
    raan: float
    arg_of_perigee: float
    mean_anomaly: float
    mean_motion: float
    period: float
    apogee: float
    perigee: float
    object_type: str
    rcs_size: str
    launch_date: str
    decay_date: str
    country: str
    def __init__(self, norad_id: _Optional[int] = ..., intl_designator: _Optional[str] = ..., sat_name: _Optional[str] = ..., line1: _Optional[str] = ..., line2: _Optional[str] = ..., epoch: _Optional[str] = ..., inclination: _Optional[float] = ..., eccentricity: _Optional[float] = ..., raan: _Optional[float] = ..., arg_of_perigee: _Optional[float] = ..., mean_anomaly: _Optional[float] = ..., mean_motion: _Optional[float] = ..., period: _Optional[float] = ..., apogee: _Optional[float] = ..., perigee: _Optional[float] = ..., object_type: _Optional[str] = ..., rcs_size: _Optional[str] = ..., launch_date: _Optional[str] = ..., decay_date: _Optional[str] = ..., country: _Optional[str] = ...) -> None: ...

class Link16Detail(_message.Message):
    __slots__ = ("track_number", "j_series_label", "originating_unit", "quality", "exercise_indicator", "simulation_indicator", "force_identity")
    TRACK_NUMBER_FIELD_NUMBER: _ClassVar[int]
    J_SERIES_LABEL_FIELD_NUMBER: _ClassVar[int]
    ORIGINATING_UNIT_FIELD_NUMBER: _ClassVar[int]
    QUALITY_FIELD_NUMBER: _ClassVar[int]
    EXERCISE_INDICATOR_FIELD_NUMBER: _ClassVar[int]
    SIMULATION_INDICATOR_FIELD_NUMBER: _ClassVar[int]
    FORCE_IDENTITY_FIELD_NUMBER: _ClassVar[int]
    track_number: int
    j_series_label: str
    originating_unit: str
    quality: int
    exercise_indicator: bool
    simulation_indicator: bool
    force_identity: str
    def __init__(self, track_number: _Optional[int] = ..., j_series_label: _Optional[str] = ..., originating_unit: _Optional[str] = ..., quality: _Optional[int] = ..., exercise_indicator: _Optional[bool] = ..., simulation_indicator: _Optional[bool] = ..., force_identity: _Optional[str] = ...) -> None: ...

class CotDetail(_message.Message):
    __slots__ = ("uid", "cot_type", "how", "ce", "le", "stale_time", "access_control", "opex", "qos")
    UID_FIELD_NUMBER: _ClassVar[int]
    COT_TYPE_FIELD_NUMBER: _ClassVar[int]
    HOW_FIELD_NUMBER: _ClassVar[int]
    CE_FIELD_NUMBER: _ClassVar[int]
    LE_FIELD_NUMBER: _ClassVar[int]
    STALE_TIME_FIELD_NUMBER: _ClassVar[int]
    ACCESS_CONTROL_FIELD_NUMBER: _ClassVar[int]
    OPEX_FIELD_NUMBER: _ClassVar[int]
    QOS_FIELD_NUMBER: _ClassVar[int]
    uid: str
    cot_type: str
    how: str
    ce: float
    le: float
    stale_time: str
    access_control: str
    opex: str
    qos: str
    def __init__(self, uid: _Optional[str] = ..., cot_type: _Optional[str] = ..., how: _Optional[str] = ..., ce: _Optional[float] = ..., le: _Optional[float] = ..., stale_time: _Optional[str] = ..., access_control: _Optional[str] = ..., opex: _Optional[str] = ..., qos: _Optional[str] = ...) -> None: ...

class UavDetail(_message.Message):
    __slots__ = ("make", "model", "serial_number", "mac_address", "operating_frequency", "frequency_range")
    MAKE_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    SERIAL_NUMBER_FIELD_NUMBER: _ClassVar[int]
    MAC_ADDRESS_FIELD_NUMBER: _ClassVar[int]
    OPERATING_FREQUENCY_FIELD_NUMBER: _ClassVar[int]
    FREQUENCY_RANGE_FIELD_NUMBER: _ClassVar[int]
    make: str
    model: str
    serial_number: str
    mac_address: str
    operating_frequency: float
    frequency_range: float
    def __init__(self, make: _Optional[str] = ..., model: _Optional[str] = ..., serial_number: _Optional[str] = ..., mac_address: _Optional[str] = ..., operating_frequency: _Optional[float] = ..., frequency_range: _Optional[float] = ...) -> None: ...

class RfDetail(_message.Message):
    __slots__ = ("frequency_hz", "bandwidth_hz", "emitter_type", "modulation", "pri_us", "pulse_width_us", "scan_type", "power_dbm")
    FREQUENCY_HZ_FIELD_NUMBER: _ClassVar[int]
    BANDWIDTH_HZ_FIELD_NUMBER: _ClassVar[int]
    EMITTER_TYPE_FIELD_NUMBER: _ClassVar[int]
    MODULATION_FIELD_NUMBER: _ClassVar[int]
    PRI_US_FIELD_NUMBER: _ClassVar[int]
    PULSE_WIDTH_US_FIELD_NUMBER: _ClassVar[int]
    SCAN_TYPE_FIELD_NUMBER: _ClassVar[int]
    POWER_DBM_FIELD_NUMBER: _ClassVar[int]
    frequency_hz: float
    bandwidth_hz: float
    emitter_type: str
    modulation: str
    pri_us: float
    pulse_width_us: float
    scan_type: str
    power_dbm: float
    def __init__(self, frequency_hz: _Optional[float] = ..., bandwidth_hz: _Optional[float] = ..., emitter_type: _Optional[str] = ..., modulation: _Optional[str] = ..., pri_us: _Optional[float] = ..., pulse_width_us: _Optional[float] = ..., scan_type: _Optional[str] = ..., power_dbm: _Optional[float] = ...) -> None: ...

class RadarDetail(_message.Message):
    __slots__ = ("range_rate_mps", "rcs_dbsm", "snr_db", "mode", "track_quality", "classification", "classification_confidence", "radar_track_id", "height_above_ground_m")
    RANGE_RATE_MPS_FIELD_NUMBER: _ClassVar[int]
    RCS_DBSM_FIELD_NUMBER: _ClassVar[int]
    SNR_DB_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    TRACK_QUALITY_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    RADAR_TRACK_ID_FIELD_NUMBER: _ClassVar[int]
    HEIGHT_ABOVE_GROUND_M_FIELD_NUMBER: _ClassVar[int]
    range_rate_mps: float
    rcs_dbsm: float
    snr_db: float
    mode: RadarMode
    track_quality: float
    classification: RadarClass
    classification_confidence: float
    radar_track_id: str
    height_above_ground_m: float
    def __init__(self, range_rate_mps: _Optional[float] = ..., rcs_dbsm: _Optional[float] = ..., snr_db: _Optional[float] = ..., mode: _Optional[_Union[RadarMode, str]] = ..., track_quality: _Optional[float] = ..., classification: _Optional[_Union[RadarClass, str]] = ..., classification_confidence: _Optional[float] = ..., radar_track_id: _Optional[str] = ..., height_above_ground_m: _Optional[float] = ...) -> None: ...

class NormalizedBox(_message.Message):
    __slots__ = ("x", "y", "width", "height")
    X_FIELD_NUMBER: _ClassVar[int]
    Y_FIELD_NUMBER: _ClassVar[int]
    WIDTH_FIELD_NUMBER: _ClassVar[int]
    HEIGHT_FIELD_NUMBER: _ClassVar[int]
    x: float
    y: float
    width: float
    height: float
    def __init__(self, x: _Optional[float] = ..., y: _Optional[float] = ..., width: _Optional[float] = ..., height: _Optional[float] = ...) -> None: ...

class EoIrDetail(_message.Message):
    __slots__ = ("band", "classification", "classification_confidence", "horizontal_fov_deg", "vertical_fov_deg", "bounding_box", "camera_track_id")
    BAND_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    HORIZONTAL_FOV_DEG_FIELD_NUMBER: _ClassVar[int]
    VERTICAL_FOV_DEG_FIELD_NUMBER: _ClassVar[int]
    BOUNDING_BOX_FIELD_NUMBER: _ClassVar[int]
    CAMERA_TRACK_ID_FIELD_NUMBER: _ClassVar[int]
    band: EoIrBand
    classification: str
    classification_confidence: float
    horizontal_fov_deg: float
    vertical_fov_deg: float
    bounding_box: NormalizedBox
    camera_track_id: str
    def __init__(self, band: _Optional[_Union[EoIrBand, str]] = ..., classification: _Optional[str] = ..., classification_confidence: _Optional[float] = ..., horizontal_fov_deg: _Optional[float] = ..., vertical_fov_deg: _Optional[float] = ..., bounding_box: _Optional[_Union[NormalizedBox, _Mapping]] = ..., camera_track_id: _Optional[str] = ...) -> None: ...

class PlatformDetail(_message.Message):
    __slots__ = ("ais", "adsb", "tle", "link16", "cot", "uav", "generic", "rf", "radar", "eoir")
    AIS_FIELD_NUMBER: _ClassVar[int]
    ADSB_FIELD_NUMBER: _ClassVar[int]
    TLE_FIELD_NUMBER: _ClassVar[int]
    LINK16_FIELD_NUMBER: _ClassVar[int]
    COT_FIELD_NUMBER: _ClassVar[int]
    UAV_FIELD_NUMBER: _ClassVar[int]
    GENERIC_FIELD_NUMBER: _ClassVar[int]
    RF_FIELD_NUMBER: _ClassVar[int]
    RADAR_FIELD_NUMBER: _ClassVar[int]
    EOIR_FIELD_NUMBER: _ClassVar[int]
    ais: AisDetail
    adsb: AdsbDetail
    tle: TleDetail
    link16: Link16Detail
    cot: CotDetail
    uav: UavDetail
    generic: GenericDetail
    rf: RfDetail
    radar: RadarDetail
    eoir: EoIrDetail
    def __init__(self, ais: _Optional[_Union[AisDetail, _Mapping]] = ..., adsb: _Optional[_Union[AdsbDetail, _Mapping]] = ..., tle: _Optional[_Union[TleDetail, _Mapping]] = ..., link16: _Optional[_Union[Link16Detail, _Mapping]] = ..., cot: _Optional[_Union[CotDetail, _Mapping]] = ..., uav: _Optional[_Union[UavDetail, _Mapping]] = ..., generic: _Optional[_Union[GenericDetail, _Mapping]] = ..., rf: _Optional[_Union[RfDetail, _Mapping]] = ..., radar: _Optional[_Union[RadarDetail, _Mapping]] = ..., eoir: _Optional[_Union[EoIrDetail, _Mapping]] = ...) -> None: ...

class GenericDetail(_message.Message):
    __slots__ = ("fields",)
    class FieldsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    fields: _containers.ScalarMap[str, str]
    def __init__(self, fields: _Optional[_Mapping[str, str]] = ...) -> None: ...
