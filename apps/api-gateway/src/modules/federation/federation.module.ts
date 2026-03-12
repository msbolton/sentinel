import { Module } from '@nestjs/common';
import { ConfigModule } from '@nestjs/config';
import { TypeOrmModule } from '@nestjs/typeorm';
import { FederationConfig, FederationPeer, FederationPolicy } from './entities';
import { SharingPolicyService } from './sharing-policy.service';
import { PeerManagerService } from './peer-manager.service';
import { FederationGateway } from './federation.gateway';
import { DiscoveryService } from './discovery.service';
import { FederationController } from './federation.controller';

@Module({
  imports: [
    ConfigModule,
    TypeOrmModule.forFeature([FederationConfig, FederationPeer, FederationPolicy]),
  ],
  controllers: [FederationController],
  providers: [
    SharingPolicyService,
    PeerManagerService,
    FederationGateway,
    DiscoveryService,
  ],
  exports: [PeerManagerService, SharingPolicyService],
})
export class FederationModule {}
