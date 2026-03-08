import { Injectable, Logger } from '@nestjs/common';
import { ObservationRepository } from './observation.repository';
import { ObservationRecord } from './observation.entity';
import { RecordObservationDto } from './dto/record-observation.dto';

@Injectable()
export class ObservationService {
  private readonly logger = new Logger(ObservationService.name);

  constructor(
    private readonly observationRepository: ObservationRepository,
  ) {}

  async record(dto: RecordObservationDto): Promise<ObservationRecord> {
    const record = this.observationRepository.create({
      entityId: dto.entityId,
      sensorId: dto.sensorId ?? null,
      feedId: dto.feedId ?? null,
      source: dto.source ?? null,
      position:
        dto.latitude != null && dto.longitude != null
          ? { type: 'Point', coordinates: [dto.longitude, dto.latitude] }
          : null,
      altitude: dto.altitude ?? null,
      heading: dto.heading ?? null,
      speedKnots: dto.speedKnots ?? null,
      course: dto.course ?? null,
      velocityNorth: dto.velocityNorth ?? null,
      velocityEast: dto.velocityEast ?? null,
      velocityUp: dto.velocityUp ?? null,
      accelNorth: dto.accelNorth ?? null,
      accelEast: dto.accelEast ?? null,
      accelUp: dto.accelUp ?? null,
      circularError: dto.circularError ?? null,
      semiMajor: dto.semiMajor ?? null,
      semiMinor: dto.semiMinor ?? null,
      ellipseOrientation: dto.ellipseOrientation ?? null,
      altitudeError: dto.altitudeError ?? null,
      detectionConfidence: dto.detectionConfidence ?? null,
      posCovariance: dto.posCovariance ?? null,
      posVelCovariance: dto.posVelCovariance ?? null,
      velCovariance: dto.velCovariance ?? null,
      azimuth: dto.azimuth ?? null,
      elevation: dto.elevation ?? null,
      range: dto.range ?? null,
      azimuthError: dto.azimuthError ?? null,
      elevationError: dto.elevationError ?? null,
      rangeError: dto.rangeError ?? null,
      trackProcessingState: dto.trackProcessingState ?? null,
      rawData: dto.rawData ?? null,
      timestamp: new Date(dto.timestamp),
    });

    return this.observationRepository.save(record);
  }

  async findByEntityId(
    entityId: string,
    limit = 100,
    offset = 0,
  ): Promise<[ObservationRecord[], number]> {
    return this.observationRepository.findByEntityId(entityId, limit, offset);
  }

  async findByEntityIdInTimeRange(
    entityId: string,
    startTime: Date,
    endTime: Date,
    limit = 1000,
  ): Promise<ObservationRecord[]> {
    return this.observationRepository.findByEntityIdInTimeRange(
      entityId,
      startTime,
      endTime,
      limit,
    );
  }
}
