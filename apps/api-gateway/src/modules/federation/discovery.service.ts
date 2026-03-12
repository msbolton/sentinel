import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { PeerManagerService } from './peer-manager.service';
import { FEDERATION_PORT_DEFAULT } from './federation.types';

@Injectable()
export class DiscoveryService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(DiscoveryService.name);
  private readonly port: number;
  private readonly psk: string | undefined;
  private mdns: unknown | null = null;
  private seedPollTimer?: ReturnType<typeof setInterval>;

  private static readonly SERVICE_TYPE = '_sentinel-fed._tcp.local';
  private static readonly MDNS_INTERVAL_MS = 30_000;
  private static readonly SEED_POLL_INTERVAL_MS = 30_000;

  constructor(
    private readonly configService: ConfigService,
    private readonly peerManager: PeerManagerService,
  ) {
    this.port = this.configService.get<number>('FEDERATION_PORT', FEDERATION_PORT_DEFAULT);
    this.psk = this.configService.get<string>('FEDERATION_PSK');
  }

  async onModuleInit(): Promise<void> {
    const config = await this.peerManager.getOrCreateConfig();
    if (!config.federationEnabled) {
      this.logger.log('Federation disabled — discovery not started');
      return;
    }

    try {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const mDNS = await import('multicast-dns' as string) as { default: () => unknown };
      this.mdns = mDNS.default();
      this.startMdnsAdvertisement(config.instanceId, config.displayName);
      this.startMdnsListening();
      this.logger.log('mDNS discovery started');
    } catch {
      this.logger.warn('multicast-dns not available — using seed list only');
    }

    const seeds = this.configService.get<string>('FEDERATION_SEEDS', '');
    if (seeds) {
      this.startSeedPolling(seeds);
    }
  }

  async onModuleDestroy(): Promise<void> {
    if (this.seedPollTimer) clearInterval(this.seedPollTimer);
    if (this.mdns && typeof (this.mdns as { destroy: () => void }).destroy === 'function') {
      (this.mdns as { destroy: () => void }).destroy();
    }
  }

  parseSeedList(seeds: string): string[] {
    if (!seeds.trim()) return [];
    return seeds.split(',').map(s => s.trim()).filter(Boolean);
  }

  buildPeerUrl(host: string, port: number): string {
    const base = `ws://${host}:${port}`;
    return this.psk ? `${base}?psk=${this.psk}` : base;
  }

  private startMdnsAdvertisement(instanceId: string, displayName: string): void {
    const mdns = this.mdns as { on: (event: string, cb: (arg: unknown) => void) => void; respond: (response: unknown) => void };

    mdns.on('query', (raw: unknown) => {
      const query = raw as { questions: Array<{ name: string }> };
      const isForUs = query.questions.some(
        (q: { name: string }) => q.name === DiscoveryService.SERVICE_TYPE,
      );
      if (!isForUs) return;

      mdns.respond({
        answers: [{
          name: DiscoveryService.SERVICE_TYPE,
          type: 'SRV',
          data: { port: this.port, target: `${instanceId}.local` },
        }, {
          name: DiscoveryService.SERVICE_TYPE,
          type: 'TXT',
          data: [`id=${instanceId}`, `name=${displayName}`],
        }],
      });
    });

    this.logger.log(`mDNS: advertising ${displayName} (${instanceId}) on port ${this.port}`);
  }

  private startMdnsListening(): void {
    const mdns = this.mdns as { on: (event: string, cb: (arg: unknown) => void) => void; query: (q: unknown) => void };

    mdns.on('response', (raw: unknown) => {
      const response = raw as { answers: Array<{ name: string; type: string; data: unknown }> };
      const srvAnswer = response.answers.find(
        (a: { name: string; type: string }) => a.name === DiscoveryService.SERVICE_TYPE && a.type === 'SRV',
      );
      const txtAnswer = response.answers.find(
        (a: { name: string; type: string }) => a.name === DiscoveryService.SERVICE_TYPE && a.type === 'TXT',
      );

      if (!srvAnswer || !txtAnswer) return;

      const srv = srvAnswer.data as { port: number; target: string };
      const txt = (txtAnswer.data as string[]).reduce((acc: Record<string, string>, entry: string) => {
        const [key, val] = entry.split('=');
        acc[key] = val;
        return acc;
      }, {});

      const peerId = txt['id'];
      if (!peerId || this.peerManager.getPeerState(peerId) !== 'disconnected') return;

      const host = srv.target.replace('.local', '');
      const url = this.buildPeerUrl(host, srv.port);
      this.logger.log(`mDNS: discovered peer ${txt['name']} (${peerId}) at ${url}`);
      this.peerManager.connectToPeer(url).catch(err => {
        this.logger.debug(`mDNS connect to ${url} failed: ${err.message}`);
      });
    });

    mdns.query({ questions: [{ name: DiscoveryService.SERVICE_TYPE, type: 'SRV' }] });

    setInterval(() => {
      mdns.query({ questions: [{ name: DiscoveryService.SERVICE_TYPE, type: 'SRV' }] });
    }, DiscoveryService.MDNS_INTERVAL_MS);

    this.logger.log('mDNS: listening for peer announcements');
  }

  private readonly activeSeedUrls = new Set<string>();

  private startSeedPolling(seeds: string): void {
    const urls = this.parseSeedList(seeds);
    this.logger.log(`Seed list: polling ${urls.length} peers every ${DiscoveryService.SEED_POLL_INTERVAL_MS}ms`);

    const pollSeeds = async () => {
      for (const url of urls) {
        if (this.activeSeedUrls.has(url)) continue;

        this.activeSeedUrls.add(url);
        this.peerManager.connectToPeer(url)
          .catch(err => {
            this.logger.debug(`Seed connect to ${url} failed: ${err.message}`);
            this.activeSeedUrls.delete(url);
          });
      }
    };

    pollSeeds();
    this.seedPollTimer = setInterval(pollSeeds, DiscoveryService.SEED_POLL_INTERVAL_MS);
  }
}
