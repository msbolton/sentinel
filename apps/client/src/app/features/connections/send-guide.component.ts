import { ChangeDetectionStrategy, Component, DestroyRef, computed, inject, input, signal } from '@angular/core';
import { DatePipe, DecimalPipe, NgTemplateOutlet } from '@angular/common';
import { copyText } from '../../shared/clipboard';
import { Feed } from '../../shared/models/feed.model';
import {
  COUNTERS,
  FRAMING_NOTES,
  PROTO_FILES,
  endpointLabel,
  goSnippet,
  jsonExample,
  netcatCommand,
  protobufExamplesFit,
  pythonSnippet,
  examplesNote,
  sendTarget,
  senderTime,
  tlsTestCommand,
} from './send-guide';

/** One entry of `status.detail.senders`. The diagnostics are newer than the rest, so each is optional. */
interface Sender {
  remote: string;
  messages: number;
  connected: boolean;
  since?: string;
  /** Left out while the sender has sent nothing; older servers send Go's zero time instead. */
  lastAt?: string;
  /** Messages from this sender that could not be decoded, why the last one could not, and its error text. */
  malformed?: number;
  lastReason?: string;
  lastError?: string;
}

/** A block of text to try or copy: its id within the guide, what it is, and the text itself. */
interface Snippet {
  key: string;
  title: string;
  text: string;
}

/** How long a button says "Copied". */
const COPIED_MS = 2000;

/** The detail comes from the server as unchecked JSON; a row the table cannot name is left out. */
function isSender(v: unknown): v is Sender {
  return typeof v === 'object' && v !== null && typeof (v as Sender).remote === 'string';
}

/** How to send to a TCP or UDP connection, and who is sending. */
@Component({
  selector: 'app-send-guide',
  standalone: true,
  imports: [DatePipe, DecimalPipe, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './send-guide.component.html',
  styleUrl: './send-guide.component.scss',
})
export class SendGuideComponent {
  readonly feed = input.required<Feed>();
  readonly protoFiles = PROTO_FILES;
  readonly senderTime = senderTime;

  /** Starts every id in this guide, so two open guides never share one. */
  readonly idBase = computed(() => `sg-${this.feed().id}`);
  /** The block whose Copy button was just used. */
  readonly copied = signal<string | null>(null);
  private copiedTimer: ReturnType<typeof setTimeout> | undefined;

  readonly target = computed(() => sendTarget(this.feed()));
  readonly endpoint = computed(() => {
    const t = this.target();
    return t ? endpointLabel(t) : '';
  });
  /** "sentinel (JSON)", "sentinel (binary protobuf)", or a device format's own name. */
  readonly formatLabel = computed(() => {
    const t = this.target();
    if (!t) return '';
    if (t.format !== 'sentinel') return t.format;
    return t.encoding === 'json' ? 'sentinel (JSON)' : 'sentinel (binary protobuf)';
  });
  readonly framingNote = computed(() => {
    const t = this.target();
    return t ? (FRAMING_NOTES[t.framing] ?? t.framing) : '';
  });

  /** A one-line command to try: netcat, or OpenSSL when the listener speaks TLS. */
  readonly testCommand = computed<Snippet | null>(() => {
    const t = this.target();
    if (!t) return null;
    if (t.tls) {
      const text = tlsTestCommand(t);
      const client = t.requireClientCert ? '; client.pem and client.key are a client certificate it accepts' : '';
      return text ? { key: 'test', title: `Try it with OpenSSL (ca.pem is the CA that signed the listener's certificate${client})`, text } : null;
    }
    const text = netcatCommand(t);
    return text ? { key: 'test', title: 'Try it with netcat', text } : null;
  });
  /** Sentinel's own JSON, whether or not a one-line command can send it. */
  readonly jsonSample = computed<Snippet | null>(() => {
    const t = this.target();
    return t && t.mode === 'listen' && t.format === 'sentinel' && t.encoding === 'json' ? { key: 'json', title: 'Sample message (JSON)', text: jsonExample() } : null;
  });
  /** The Go and Python senders write binary Sentinel protobuf in one framing, so they only fit that. */
  readonly showExamples = computed(() => {
    const t = this.target();
    return t !== null && t.mode === 'listen' && protobufExamplesFit(t);
  });
  readonly go = computed<Snippet | null>(() => {
    const t = this.target();
    return t ? { key: 'go', title: 'Go', text: goSnippet(t) } : null;
  });
  readonly python = computed<Snippet | null>(() => {
    const t = this.target();
    return t ? { key: 'python', title: 'Python', text: pythonSnippet(t) } : null;
  });
  /** Why a listening connection has no Go and Python examples; empty when it has them. */
  readonly noExamplesNote = computed(() => {
    const t = this.target();
    return t && t.mode === 'listen' ? examplesNote(t) : '';
  });

  readonly lastError = computed(() => this.feed().status?.lastError);
  private readonly detail = computed<Record<string, unknown>>(() => this.feed().status?.detail ?? {});
  readonly allowOpen = computed(() => {
    const t = this.target();
    return t !== null && t.mode === 'listen' && t.allow.length === 0;
  });
  /** When the connection Sentinel dialled came up; the server leaves it out while the connection is down. */
  readonly connectedSince = computed(() => {
    const since = this.detail()['since'];
    return typeof since === 'string' ? senderTime(since) : null;
  });

  readonly senders = computed<Sender[]>(() => {
    const senders = this.detail()['senders'];
    return Array.isArray(senders) ? senders.filter(isSender) : [];
  });
  /** How many senders there are, which can be more than the list holds. */
  readonly distinctSenders = computed(() => {
    const n = this.detail()['distinctSenders'];
    return typeof n === 'number' ? n : this.senders().length;
  });
  /** Only a TCP sender connects, so only TCP has a time it connected since. */
  readonly showSince = computed(() => {
    const t = this.target();
    return t !== null && t.mode === 'listen' && t.protocol === 'tcp';
  });
  /** The undecodable columns appear once any sender has something to show in them. */
  readonly hasDiagnostics = computed(() => this.senders().some((s) => (s.malformed ?? 0) > 0 || !!s.lastReason));
  readonly counters = computed(() => {
    const t = this.target();
    const detail = this.detail();
    if (!t) return [];
    // Only what applies to this connection, and only what the server reports.
    return COUNTERS.filter((c) => c.applies(t)).flatMap((c) => {
      const value = detail[c.key];
      return typeof value === 'number' ? [{ ...c, value }] : [];
    });
  });

  constructor() {
    inject(DestroyRef).onDestroy(() => clearTimeout(this.copiedTimer));
  }

  /** Copies a block to the clipboard; if the browser refuses, selects it so it can be copied by hand. */
  async copy(snippet: Snippet, pre: HTMLElement): Promise<void> {
    clearTimeout(this.copiedTimer);
    if (await copyText(snippet.text)) {
      this.copied.set(snippet.key);
      this.copiedTimer = setTimeout(() => this.copied.set(null), COPIED_MS);
      return;
    }
    this.copied.set(null);
    pre.focus();
    window.getSelection()?.selectAllChildren(pre);
  }
}
