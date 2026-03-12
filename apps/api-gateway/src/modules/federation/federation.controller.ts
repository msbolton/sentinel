import {
  Controller,
  Get,
  Put,
  Post,
  Delete,
  Body,
  Param,
  UseGuards,
} from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { PeerManagerService } from './peer-manager.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

interface UpdateConfigDto {
  displayName?: string;
  federationEnabled?: boolean;
}

interface AddSeedPeerDto {
  url: string;
  displayName: string;
}

interface UpdatePolicyDto {
  entityTypesAllowed?: string[];
  geoBounds?: { north: number; south: number; east: number; west: number } | null;
  enabled?: boolean;
}

@Controller('api/v1/federation')
@UseGuards(JwtAuthGuard)
@Roles('sentinel-admin')
export class FederationController {
  constructor(
    private readonly peerManager: PeerManagerService,
    @InjectRepository(FederationConfig)
    private readonly configRepo: Repository<FederationConfig>,
    @InjectRepository(FederationPeer)
    private readonly peerRepo: Repository<FederationPeer>,
    @InjectRepository(FederationPolicy)
    private readonly policyRepo: Repository<FederationPolicy>,
  ) {}

  @Get('config')
  async getConfig(): Promise<FederationConfig> {
    return this.peerManager.getOrCreateConfig();
  }

  @Put('config')
  async updateConfig(@Body() body: UpdateConfigDto): Promise<FederationConfig> {
    const config = await this.peerManager.getOrCreateConfig();
    if (body.displayName !== undefined) config.displayName = body.displayName;
    if (body.federationEnabled !== undefined) config.federationEnabled = body.federationEnabled;
    return this.configRepo.save(config);
  }

  @Get('peers')
  async getPeers(): Promise<FederationPeer[]> {
    return this.peerRepo.find();
  }

  @Get('status')
  async getStatus(): Promise<{ connectedPeers: Array<{ instanceId: string; displayName: string; ceiling: string }> }> {
    return { connectedPeers: this.peerManager.getConnectedPeers() };
  }

  @Post('peers')
  async addSeedPeer(@Body() body: AddSeedPeerDto): Promise<FederationPeer> {
    const peer = new FederationPeer();
    peer.instanceId = crypto.randomUUID();
    peer.url = body.url;
    peer.displayName = body.displayName;
    peer.classificationLevel = 'classification-u';
    peer.status = 'disconnected';
    peer.isSeed = true;
    return this.peerRepo.save(peer);
  }

  @Delete('peers/:instanceId')
  async removePeer(@Param('instanceId') instanceId: string): Promise<{ message: string }> {
    await this.policyRepo.delete({ peerInstanceId: instanceId });
    await this.peerRepo.delete({ instanceId });
    return { message: `Peer ${instanceId} removed` };
  }

  @Put('policies/:peerInstanceId')
  async updatePolicy(
    @Param('peerInstanceId') peerInstanceId: string,
    @Body() body: UpdatePolicyDto,
  ): Promise<FederationPolicy> {
    let policy = await this.policyRepo.findOne({ where: { peerInstanceId } });

    if (!policy) {
      policy = this.policyRepo.create({ peerInstanceId });
    }

    if (body.entityTypesAllowed !== undefined) policy.entityTypesAllowed = body.entityTypesAllowed;
    if (body.geoBounds !== undefined) policy.geoBounds = body.geoBounds;
    if (body.enabled !== undefined) policy.enabled = body.enabled;

    return this.policyRepo.save(policy);
  }

  @Get('policies/:peerInstanceId')
  async getPolicy(@Param('peerInstanceId') peerInstanceId: string): Promise<FederationPolicy | null> {
    return this.policyRepo.findOne({ where: { peerInstanceId } });
  }
}
