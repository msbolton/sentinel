import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FieldHint, SchemaFormChange, SchemaFormComponent } from '../../shared/components/schema-form/schema-form.component';
import { ListenPorts } from '../../shared/models/feed.model';
import { schemaOf } from '../../shared/testing/driver-schemas';
import { connectionHints } from './connection-hints';
import { listenOf } from './listen-config';

/**
 * A hint is addressed by pointer, and a field that is not shown has no place for one, so a driver schema that
 * renames or moves the port or the allowlist would silence the hints without a word. These specs render the
 * schemas the ingest drivers really ship with the hints `connectionHints` makes for a listener of each kind.
 */
describe('connectionHints with the driver schemas', () => {
  let fixture: ComponentFixture<SchemaFormComponent>;
  let el: HTMLElement;
  let changes: SchemaFormChange[];

  const ports: ListenPorts = {
    min: 1024,
    max: 65535,
    reserved: [{ from: 3000, to: 3005 }],
    taken: [
      { protocol: 'tcp', port: 6001, feedId: 'radar', feedName: 'Radar feed', enabled: true },
      { protocol: 'udp', port: 10110, feedId: 'pier', feedName: 'Pier AIS', enabled: true },
    ],
    suggested: { tcp: 6002, udp: 10111 },
  };

  /** Renders a driver's form on a stored config, with the hints for it. */
  function render(driver: string, config: Record<string, unknown>): FieldHint[] {
    const hints = connectionHints({ driverId: driver, config, ports });
    fixture.componentRef.setInput('schema', schemaOf(driver));
    fixture.componentRef.setInput('value', config);
    fixture.componentRef.setInput('hints', hints);
    fixture.detectChanges();
    return hints;
  }

  function hintAt(pointer: string): HTMLElement | null {
    return el.querySelector<HTMLElement>(`[data-pointer="${pointer}"] > .sf-hint`);
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [SchemaFormComponent] }).compileComponents();
    fixture = TestBed.createComponent(SchemaFormComponent);
    el = fixture.nativeElement as HTMLElement;
    changes = [];
    fixture.componentInstance.changed.subscribe((c) => changes.push(c));
  });

  it('tcp, udp, grpc and stream: the port and allowlist hints land on fields the schema shows', () => {
    const listeners = [
      { driver: 'tcp', protocol: 'tcp', taken: 6001, config: (port?: number) => ({ mode: 'listen', port }) },
      { driver: 'udp', protocol: 'udp', taken: 10110, config: (port?: number) => ({ port }) },
      { driver: 'grpc', protocol: 'tcp', taken: 6001, config: (port?: number) => ({ port }) },
      {
        driver: 'stream',
        protocol: 'tcp',
        taken: 6001,
        config: (port?: number) => ({ transport: { kind: 'tcpserver', address: port ? `:${port}` : undefined } }),
      },
      {
        driver: 'stream',
        protocol: 'udp',
        taken: 10110,
        config: (port?: number) => ({ transport: { kind: 'udp', address: port ? `:${port}` : undefined } }),
      },
    ] as const;

    for (const { driver, protocol, taken, config } of listeners) {
      // no port yet: a free one to take; a taken port: a warning, with a free one to take instead
      for (const stored of [config(), config(taken)]) {
        const { portPath, allowPath } = listenOf(driver, stored)!;
        const hints = render(driver, stored);
        expect(hints.map((h) => h.path)).toEqual([portPath, allowPath]);
        for (const hint of hints) {
          expect(hintAt(hint.path)?.textContent).toContain(hint.message);
        }

        // taking the port leaves a config the server reads as listening on it, and a field to carry on in
        hintAt(portPath)!.querySelector('button')!.click();
        fixture.detectChanges();
        const last = changes[changes.length - 1].config;
        expect(listenOf(driver, last)?.port).toBe(ports.suggested[protocol]);
        expect(connectionHints({ driverId: driver, config: last, ports }).map((h) => h.path)).toEqual([allowPath]);
        expect(document.activeElement).toBe(el.querySelector(`[data-pointer="${portPath}"] input`));
      }
    }
  });

  it('tcp, grpc and stream: the TLS settings the schema offers make mutual TLS, and an open allowlist information', () => {
    const form = () => fixture.componentInstance;
    const hintsFor = (driver: string, config: Record<string, unknown>) => {
      fixture.componentRef.setInput('hints', connectionHints({ driverId: driver, config, ports }));
      fixture.detectChanges();
    };

    for (const [driver, config, tls] of [
      ['tcp', { mode: 'listen', port: 6100 }, '/config/tls'],
      ['grpc', { port: 6100 }, '/config/tls'],
      ['stream', { transport: { kind: 'tcpserver', address: ':6100' } }, '/config/transport/tls'],
    ] as const) {
      const { allowPath } = listenOf(driver, config)!;
      render(driver, config);
      expect(hintAt(allowPath)?.classList.contains('sf-hint-warn')).toBe(true);

      // TLS alone proves the listener to the sender, not the sender to the listener
      form().control(`${tls}/enabled`)!.setValue(true);
      expect(listenOf(driver, changes[changes.length - 1].config)!.mutualTls).toBe(false);
      hintsFor(driver, changes[changes.length - 1].config);
      expect(hintAt(allowPath)?.classList.contains('sf-hint-warn')).toBe(true);

      // asking for client certificates, as a user would in the form, is what makes it mutual
      form().control(`${tls}/requireClientCert`)!.setValue(true);
      const edited = changes[changes.length - 1].config;
      expect(listenOf(driver, edited)!.mutualTls).toBe(true);
      hintsFor(driver, edited);
      expect(hintAt(allowPath)?.classList.contains('sf-hint-warn')).toBe(false);
      expect(hintAt(allowPath)?.textContent).toContain('only senders with a certificate signed by the client CA get in');
    }
  });
});
