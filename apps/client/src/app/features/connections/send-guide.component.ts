import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { DatePipe } from '@angular/common';
import { Feed } from '../../shared/models/feed.model';
import { endpointLabel, goSnippet, netcatCommand, PROTO_FILES, pythonSnippet, sendTarget } from './send-guide';

/** One entry of `status.detail.senders`. */
interface Sender {
  remote: string;
  messages: number;
  lastAt: string;
  connected: boolean;
}

/** The counters worth showing, in the order they appear. */
const COUNTER_KEYS = ['messages', 'accepted', 'rejected', 'refused', 'rateLimited', 'oversize', 'malformed', 'tlsFailed', 'reconnects'];

/** How to send to a TCP or UDP connection, and who is sending. */
@Component({
  selector: 'app-send-guide',
  standalone: true,
  imports: [DatePipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './send-guide.component.html',
  styleUrl: './send-guide.component.scss',
})
export class SendGuideComponent {
  readonly feed = input.required<Feed>();
  readonly protoFiles = PROTO_FILES;

  readonly target = computed(() => sendTarget(this.feed()));
  readonly endpoint = computed(() => {
    const t = this.target();
    return t ? endpointLabel(t) : '';
  });
  /** The wire format of Sentinel's own messages; empty for the device formats. */
  readonly encodingNote = computed(() => {
    const t = this.target();
    if (!t || t.format !== 'sentinel') return '';
    if (t.encoding === 'json') return 'JSON, one message per line';
    return t.protocol === 'tcp' ? 'binary protobuf, length-delimited' : 'binary protobuf, one message per datagram';
  });
  readonly netcat = computed(() => {
    const t = this.target();
    return t ? netcatCommand(t) : null;
  });
  /** The Go and Python senders write binary Sentinel protobuf, so they only fit that format. */
  readonly showExamples = computed(() => {
    const t = this.target();
    return t !== null && t.format === 'sentinel' && t.encoding === 'protobuf';
  });
  readonly go = computed(() => {
    const t = this.target();
    return t ? goSnippet(t) : '';
  });
  readonly python = computed(() => {
    const t = this.target();
    return t ? pythonSnippet(t) : '';
  });

  readonly lastError = computed(() => this.feed().status?.lastError);
  private readonly detail = computed<Record<string, unknown>>(() => this.feed().status?.detail ?? {});
  readonly allowOpen = computed(() => {
    const allow = this.feed().config?.['allow'];
    return this.target()?.mode === 'listen' && !(Array.isArray(allow) && allow.length > 0);
  });
  readonly senders = computed<Sender[]>(() => {
    const senders = this.detail()['senders'];
    return Array.isArray(senders) ? (senders as Sender[]) : [];
  });
  readonly counters = computed(() =>
    COUNTER_KEYS.filter((key) => this.detail()[key] !== undefined).map((key) => ({ key, value: this.detail()[key] })),
  );
}
