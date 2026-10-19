import { ComponentFixture, TestBed } from '@angular/core/testing';
import { formatDate } from '@angular/common';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { DeadLetter, DeadLetterPage } from '../../shared/models/feed.model';
import { DeadLettersComponent } from './dead-letters.component';

const LIST = '/api/v1/feeds/sensor-tcp/deadletters?limit=20';
const WITH_PAYLOAD = `${LIST}&payload=true`;

/** The fields an older server may leave out of a reject altogether. */
type Unrecorded = 'error' | 'driver' | 'decoder' | 'sender' | 'payload' | 'payloadEncoding' | 'payloadTruncated';

/** What Go writes for a `time.Time` nobody set: the way the server says it does not know when a reject came. */
const ZERO_TIME = '0001-01-01T00:00:00Z';

/** A reject as the server lists it (Task 23's contract); the payload fields come only with `payload=true`. */
function letter(over: Partial<DeadLetter> = {}): DeadLetter {
  return {
    at: '2026-10-18T12:00:00Z',
    reason: 'parse_error',
    error: 'not a sentinel.v1.Observation in JSON: …',
    driver: 'tcp',
    decoder: 'sentinel',
    sender: '10.0.0.5:51234',
    size: 17,
    partition: 0,
    offset: 42,
    ...over,
  };
}

/** The same reject with its payload, as `payload=true` answers. */
function withPayload(over: Partial<DeadLetter> = {}): DeadLetter {
  return letter({ payload: 'not json', payloadEncoding: 'utf8', payloadTruncated: false, ...over });
}

/** A reject as an older server wrote it: without some of its fields, not with them empty. */
function without(l: DeadLetter, ...fields: Unrecorded[]): DeadLetter {
  const older = { ...l };
  for (const field of fields) {
    delete older[field];
  }
  return older;
}

function page(letters: DeadLetter[], over: Partial<DeadLetterPage> = {}): DeadLetterPage {
  return { letters, scanned: 120, partial: false, ...over };
}

/** How the panel writes a time: the date pipe's format, in the browser's zone, which is also the test's. */
const timeOf = (iso: string) => formatDate(iso, 'MMM d HH:mm:ss', 'en-US');

describe('DeadLettersComponent', () => {
  let fixture: ComponentFixture<DeadLettersComponent>;
  let el: HTMLElement;
  let http: HttpTestingController;

  /** A fresh panel for a connection; the one before it, if any, is gone. */
  function render(feedId = 'sensor-tcp'): void {
    fixture?.destroy();
    fixture = TestBed.createComponent(DeadLettersComponent);
    fixture.componentRef.setInput('feedId', feedId);
    fixture.detectChanges();
    el = fixture.nativeElement as HTMLElement;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [DeadLettersComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    http = TestBed.inject(HttpTestingController);
    render();
  });

  afterEach(() => {
    fixture.destroy();
    http.verify();
  });

  const text = (node: Element | null | undefined) => (node?.textContent ?? '').replace(/\s+/g, ' ').trim();
  const status = () => text(el.querySelector('[role=status]'));
  const headers = () => Array.from(el.querySelectorAll('th')).map(text);
  const rows = () => Array.from(el.querySelectorAll<HTMLTableRowElement>('tbody tr.reject'));
  const cellsOf = (tr: HTMLTableRowElement) => Array.from(tr.cells).map(text);
  const button = (name: string) => Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => text(b) === name) ?? null;
  const pre = () => Array.from(el.querySelectorAll('pre'));
  /** Nothing is asking the server anything that has not been looked at yet. */
  const noRequest = () => http.expectNone(() => true);

  function press(name: string): void {
    const b = button(name);
    if (!b) {
      throw new Error(`no "${name}" button`);
    }
    b.click();
    fixture.detectChanges();
  }

  /** Clicks the summary, as a person does to open or close the panel, and waits for the toggle event the browser sends. */
  async function toggle(): Promise<void> {
    const details = el.querySelector('details')!;
    const toggled = new Promise<void>((resolve) => details.addEventListener('toggle', () => resolve(), { once: true }));
    el.querySelector<HTMLElement>('summary')!.click();
    await toggled;
    fixture.detectChanges();
  }

  /** Opens the panel and answers its first request. */
  async function openWith(answer: DeadLetterPage): Promise<void> {
    await toggle();
    http.expectOne(LIST).flush(answer);
    fixture.detectChanges();
  }

  /** Opens the panel and fails its first request. */
  async function openFailing(code: number): Promise<void> {
    await toggle();
    http.expectOne(LIST).flush({ error: 'no' }, { status: code, statusText: 'No' });
    fixture.detectChanges();
  }

  it('loads rejects on first open only', async () => {
    // Nothing is asked while the panel is closed, and the summary names it.
    noRequest();
    expect(text(el.querySelector('summary'))).toBe('Recent rejects');
    expect(el.querySelector('details')!.open).toBe(false);

    await toggle();
    const first = http.expectOne(LIST);
    expect(first.request.method).toBe('GET');
    expect(status()).toBe('Loading rejects…');
    first.flush(page([letter()]));
    fixture.detectChanges();
    expect(rows()).toHaveLength(1);

    // Closing it and opening it again shows what is there; it does not ask again.
    await toggle();
    expect(el.querySelector('details')!.open).toBe(false);
    await toggle();
    expect(el.querySelector('details')!.open).toBe(true);
    noRequest();
    expect(rows()).toHaveLength(1);

    // Refresh does.
    press('Refresh');
    http.expectOne(LIST).flush(page([letter(), letter({ offset: 43 })]));
    fixture.detectChanges();
    expect(rows()).toHaveLength(2);
  });

  it('asks about its own connection', async () => {
    render('other-link');
    await toggle();
    http.expectOne('/api/v1/feeds/other-link/deadletters?limit=20').flush(page([]));
  });

  it('shows reason, sender and error per row', async () => {
    await openWith(
      page([
        letter(),
        letter({
          at: '2026-10-18T11:58:30Z',
          reason: 'oversize',
          error: 'frame of 70000 bytes exceeds the 65536 limit',
          decoder: 'nmea-ais',
          sender: '10.0.0.6:4001',
          size: 70000,
          offset: 41,
        }),
      ]),
    );

    expect(status()).toBe('2 rejects in the last 120 scanned, newest first');
    expect(headers()).toEqual(['Time', 'Reason', 'Decoder', 'Sender', 'Error', 'Size']);
    // in the order the server gives them: newest first
    expect(rows().map(cellsOf)).toEqual([
      [timeOf('2026-10-18T12:00:00Z'), 'parse_error', 'sentinel', '10.0.0.5:51234', 'not a sentinel.v1.Observation in JSON: …', '17 B'],
      [timeOf('2026-10-18T11:58:30Z'), 'oversize', 'nmea-ais', '10.0.0.6:4001', 'frame of 70000 bytes exceeds the 65536 limit', '70,000 B'],
    ]);

    // The Error cell keeps the line breaks of what it says, so any space round the text in the page would be shown.
    expect(rows().map((row) => row.querySelector('td.error')!.textContent)).toEqual([
      'not a sentinel.v1.Observation in JSON: …',
      'frame of 70000 bytes exceeds the 65536 limit',
    ]);

    // The time is a time: exact in the machine-readable attribute, and with where it sits in the topic for anyone who goes looking.
    const time = rows()[0].querySelector('time')!;
    expect(time.getAttribute('datetime')).toBe('2026-10-18T12:00:00Z');
    expect(time.getAttribute('title')).toBe('2026-10-18T12:00:00Z (partition 0, offset 42)');
  });

  it('lets the keyboard reach a table wider than the panel, and names it for the one it belongs to', async () => {
    await openWith(page([letter()]));

    const scroll = el.querySelector('.scroll')!;
    expect(scroll.getAttribute('tabindex')).toBe('0');
    expect(scroll.getAttribute('role')).toBe('region');
    // the page has one panel per connection, so the name says which
    expect(scroll.getAttribute('aria-label')).toBe('Recent rejects table of sensor-tcp');
    expect(scroll.contains(el.querySelector('table'))).toBe(true);
  });

  it('is a table a screen reader can read: a caption and column headers', async () => {
    await openWith(page([letter()]));

    const table = el.querySelector('table')!;
    expect(text(table.querySelector('caption'))).toBe('Recent rejects, newest first');
    const headings = Array.from(table.querySelectorAll('th'));
    expect(headings).toHaveLength(6);
    expect(headings.map((th) => th.getAttribute('scope'))).toEqual(Array(6).fill('col'));
    expect(status()).toBe('1 reject in the last 120 scanned, newest first');
  });

  it('shows a dash for what a reject does not record: a key left out, the empty text the server writes, the zero time', async () => {
    await openWith(
      page([
        // from a server that did not record the sender and the decoder, and left their keys out
        without(letter({ offset: 7 }), 'sender', 'decoder', 'driver', 'error'),
        // from one that writes every key, "" for what it does not know (an unknown sender, say)
        letter({ offset: 6, sender: '', decoder: '', driver: '', error: '' }),
        // and Go's zero time for a time it does not know
        letter({ offset: 5, at: ZERO_TIME }),
        letter(),
      ]),
    );

    const [absent, empty, noTime, current] = rows().map(cellsOf);
    const unknown = [timeOf('2026-10-18T12:00:00Z'), 'parse_error', '—', '—', '—', '17 B'];
    expect(absent).toEqual(unknown);
    expect(empty).toEqual(unknown);
    expect(noTime).toEqual(['—', ...current.slice(1)]);
    expect(current[2]).toBe('sentinel');
    // a dash says it is missing; it is not a value
    expect(rows().map((row) => row.querySelectorAll('td [title="Not recorded"]').length)).toEqual([3, 3, 1, 0]);
    // and there is no time to make of the zero one
    expect(rows().map((row) => row.querySelector('time') !== null)).toEqual([true, true, false, true]);
  });

  // The decoder's words, the payload and the sender's address all come from whoever sent the message.
  it('shows what a sender controls as the text it is, whatever markup it holds', async () => {
    const image = '<img src=x onerror=alert(1)>';
    const script = '</pre><script>x</script>';
    await openWith(page([letter({ error: image, sender: script, decoder: image })]));
    press('Show payload');
    http
      .expectOne(WITH_PAYLOAD)
      .flush(page([withPayload({ error: image, sender: script, decoder: image, payload: `${script}${image}` })]));
    fixture.detectChanges();

    // none of it became an element, and none of it closed one of the panel's own
    expect(el.querySelector('img,script')).toBeNull();
    expect(el.querySelector('[onerror]')).toBeNull();
    expect(el.querySelectorAll('table')).toHaveLength(1);
    expect(pre()).toHaveLength(1);
    // it is all there as it was sent
    const [row] = rows();
    expect(text(row.cells[2])).toBe(image);
    expect(text(row.cells[3])).toBe(script);
    expect(row.cells[4].textContent).toBe(image);
    expect(pre()[0].textContent).toBe(`${script}${image}`);
  });

  it('fetches the payload on request', async () => {
    await openWith(page([letter(), letter({ at: '2026-10-18T11:58:30Z', offset: 41, size: 8 })]));
    expect(pre()).toHaveLength(0);

    press('Show payload');
    const req = http.expectOne(WITH_PAYLOAD);
    expect(req.request.method).toBe('GET');
    // the rows stay where they are while it comes, with no empty payload rows in the meantime
    expect(rows()).toHaveLength(2);
    expect(el.querySelectorAll('tr.payload')).toHaveLength(0);
    expect(status()).toBe('Loading rejects…');
    // the table says it is being replaced, and the status line is not part of that, so it is announced
    expect(el.querySelector('.scroll')!.getAttribute('aria-busy')).toBe('true');
    expect(el.querySelector('[role=status]')!.closest('[aria-busy=true]')).toBeNull();
    req.flush(page([withPayload(), withPayload({ at: '2026-10-18T11:58:30Z', offset: 41, size: 8, payload: '{"id":' })]));
    fixture.detectChanges();

    // each payload is in a block of its own the keyboard can reach, under its row
    expect(pre().map((p) => p.textContent)).toEqual(['not json', '{"id":']);
    expect(pre().map((p) => p.getAttribute('tabindex'))).toEqual(['0', '0']);
    expect(Array.from(el.querySelectorAll('tbody tr')).map((tr) => tr.className)).toEqual(['reject', 'payload', 'reject', 'payload']);
    expect(text(el.querySelector('figure figcaption'))).toBe('Payload');
    expect(el.querySelector('.scroll')!.getAttribute('aria-busy')).toBe('false');
    expect(button('Show payload')).toBeNull();

    // Hiding them asks for nothing: they are not shown, and Refresh does not ask for them either.
    press('Hide payload');
    noRequest();
    expect(pre()).toHaveLength(0);
    expect(button('Show payload')).not.toBeNull();
    press('Refresh');
    http.expectOne(LIST).flush(page([letter(), letter({ offset: 41 })]));
    fixture.detectChanges();
    expect(pre()).toHaveLength(0);
  });

  it('keeps asking for the payload while it is shown', async () => {
    await openWith(page([letter()]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(page([withPayload()]));
    fixture.detectChanges();

    press('Refresh');
    http.expectOne(WITH_PAYLOAD).flush(page([withPayload({ payload: 'again' })]));
    fixture.detectChanges();
    expect(pre().map((p) => p.textContent)).toEqual(['again']);
  });

  it('shows a binary payload as base64 and says when it was cut', async () => {
    await openWith(page([letter()]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(
      page([
        withPayload({ offset: 44, size: 3, payload: 'AP+A', payloadEncoding: 'base64' }),
        withPayload({ offset: 43, size: 4096, payload: 'GET / HTTP/1.1', payloadTruncated: true }),
        withPayload({ offset: 42, size: 9000, payload: 'AAECAw==', payloadEncoding: 'base64', payloadTruncated: true }),
      ]),
    );
    fixture.detectChanges();

    const figures = Array.from(el.querySelectorAll('figure'));
    expect(figures.map((f) => text(f.querySelector('figcaption')))).toEqual([
      'Payload · binary (base64)',
      'Payload · cut to 1 KiB of 4,096 bytes',
      'Payload · binary (base64) · cut to 1 KiB of 9,000 bytes',
    ]);
    // the text itself follows the label, as it came
    expect(figures.map((f) => f.querySelector('pre')!.textContent)).toEqual(['AP+A', 'GET / HTTP/1.1', 'AAECAw==']);
  });

  it('names each payload block for the reject it belongs to, and describes how it is shown', async () => {
    await openWith(page([letter()]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(
      page([
        withPayload(),
        withPayload({
          at: '2026-10-18T11:58:30Z',
          reason: 'oversize',
          size: 9000,
          offset: 41,
          payload: 'AAECAw==',
          payloadEncoding: 'base64',
          payloadTruncated: true,
        }),
        withPayload({ at: ZERO_TIME, offset: 40 }),
      ]),
    );
    fixture.detectChanges();

    const blocks = pre();
    // the name says which reject, as its row does; a reject with no time is named by what is left
    expect(blocks.map((p) => p.getAttribute('aria-label'))).toEqual([
      `Payload of the reject at ${timeOf('2026-10-18T12:00:00Z')} (parse_error), partition 0, offset 42`,
      `Payload of the reject at ${timeOf('2026-10-18T11:58:30Z')} (oversize), partition 0, offset 41`,
      'Payload of the reject (parse_error), partition 0, offset 40',
    ]);
    // and the caption, which says how the text is encoded or cut, is read after it
    const described = blocks.map((p) => document.getElementById(p.getAttribute('aria-describedby') ?? '')?.textContent?.trim());
    expect(described).toEqual(['Payload', 'Payload · binary (base64) · cut to 1 KiB of 9,000 bytes', 'Payload']);
    // the ids are the page's own, one per block
    expect(new Set(blocks.map((p) => p.getAttribute('aria-describedby'))).size).toBe(3);
    expect(blocks.every((p) => p.getAttribute('role') === 'group' && p.getAttribute('tabindex') === '0')).toBe(true);
    expect(blocks.some((p) => p.hasAttribute('aria-labelledby'))).toBe(false);
  });

  it('gives two rejects of the same second and reason different payload names', async () => {
    await openWith(page([letter({ offset: 42 })]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(page([withPayload({ offset: 42 }), withPayload({ offset: 41 }), withPayload({ partition: 3, offset: 41 })]));
    fixture.detectChanges();

    const names = pre().map((p) => p.getAttribute('aria-label'));
    expect(new Set(names).size).toBe(3);
    expect(names.map((n) => n!.replace(/^.*\), /, ''))).toEqual(['partition 0, offset 42', 'partition 0, offset 41', 'partition 3, offset 41']);
  });

  it('says so when a reject has no payload, or an empty one', async () => {
    await openWith(page([letter()]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(
      page([withPayload({ payload: '', size: 0 }), without(withPayload({ offset: 41 }), 'payload', 'payloadEncoding')]),
    );
    fixture.detectChanges();

    expect(Array.from(el.querySelectorAll('tr.payload')).map(text)).toEqual(['Payload · empty', 'Payload —']);
    // nothing to read, nothing to focus
    expect(pre()).toHaveLength(0);
  });

  it('explains 403 and 501', async () => {
    await openFailing(403);
    expect(status()).toBe("You are not cleared to see this connection's rejects");
    // there is nothing to try again, and nothing to list
    expect(button('Retry')).toBeNull();
    expect(button('Refresh')).toBeNull();
    expect(el.querySelector('table')).toBeNull();

    render();
    await openFailing(501);
    expect(status()).toBe('Dead-letter viewing is not enabled on this server');
    expect(button('Retry')).toBeNull();
    expect(button('Refresh')).toBeNull();
    expect(el.querySelector('table')).toBeNull();
  });

  it('says so when the connection is gone, and offers no way to ask again', async () => {
    await openFailing(404);
    expect(status()).toBe('This connection no longer exists');
    expect(button('Retry')).toBeNull();
    expect(button('Refresh')).toBeNull();
    expect(el.querySelector('table')).toBeNull();

    // as when the connection goes while a list is on show
    render();
    await openWith(page([letter()]));
    press('Refresh');
    http.expectOne(LIST).flush({ error: 'not found' }, { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();
    expect(status()).toBe('This connection no longer exists');
    expect(button('Show payload')).toBeNull();
    expect(el.querySelector('table')).toBeNull();
  });

  it("says it couldn't load the rejects, and asks again on Retry", async () => {
    await openFailing(502);
    expect(status()).toBe("Couldn't load rejects");
    expect(el.querySelector('table')).toBeNull();
    // one button for it, which says what it does now
    expect(button('Refresh')).toBeNull();

    press('Retry');
    expect(status()).toBe('Loading rejects…');
    http.expectOne(LIST).flush(page([letter()]));
    fixture.detectChanges();
    expect(rows()).toHaveLength(1);
    expect(button('Retry')).toBeNull();
    expect(button('Refresh')).not.toBeNull();
  });

  it("says the same when the network is down", async () => {
    await toggle();
    http.expectOne(LIST).error(new ProgressEvent('error'));
    fixture.detectChanges();

    expect(status()).toBe("Couldn't load rejects");
    expect(button('Retry')).not.toBeNull();
  });

  it('drops the list when a refresh fails, as it can no longer vouch for it', async () => {
    await openWith(page([letter()]));
    expect(rows()).toHaveLength(1);

    press('Refresh');
    http.expectOne(LIST).flush('down', { status: 504, statusText: 'Gateway Timeout' });
    fixture.detectChanges();

    expect(status()).toBe("Couldn't load rejects");
    expect(rows()).toHaveLength(0);
    expect(el.querySelector('table')).toBeNull();
  });

  it('keeps focus on its button as the button turns from Refresh into Retry and back', async () => {
    await openWith(page([letter()]));
    const refresh = button('Refresh')!;
    refresh.focus();

    press('Refresh');
    http.expectOne(LIST).flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    expect(button('Retry')).toBe(refresh);
    expect(document.activeElement).toBe(refresh);

    press('Retry');
    http.expectOne(LIST).flush(page([letter()]));
    fixture.detectChanges();
    expect(button('Refresh')).toBe(refresh);
    expect(document.activeElement).toBe(refresh);
  });

  // The failure takes the list away, and with it the button that says whether the payloads are on. A Retry that
  // asked for them would ask for what nothing shows as asked for, so it asks for the plain list, and Show payload
  // is there to ask again.
  it('asks for the plain list when Retry follows a failed request for the payloads', async () => {
    await openWith(page([letter()]));
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    expect(status()).toBe("Couldn't load rejects");

    press('Retry');
    http.expectOne(LIST).flush(page([letter()]));
    fixture.detectChanges();
    expect(rows()).toHaveLength(1);
    expect(pre()).toHaveLength(0);
    expect(button('Show payload')).not.toBeNull();

    // and they come when they are asked for again
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(page([withPayload()]));
    fixture.detectChanges();
    expect(pre().map((p) => p.textContent)).toEqual(['not json']);
  });

  it('keeps asking for the plain list when a failed refresh of it is retried', async () => {
    await openWith(page([letter()]));
    press('Refresh');
    http.expectOne(LIST).flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    press('Retry');
    http.expectOne(LIST).flush(page([letter()]));
    fixture.detectChanges();
    expect(rows()).toHaveLength(1);
  });

  // The failure takes the list away, and the Show payload button that belonged to it; a keyboard user who had just
  // pressed it must not be sent back to the top of the page.
  it('moves the focus to Retry when the failure removes the button it was on', async () => {
    await openWith(page([letter()]));
    button('Show payload')!.focus();
    press('Show payload');
    expect(document.activeElement).toBe(button('Hide payload'));

    http.expectOne(WITH_PAYLOAD).flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(button('Hide payload')).toBeNull();
    expect(button('Show payload')).toBeNull();
    expect(document.activeElement).toBe(button('Retry'));
  });

  it('leaves the focus alone when it was not on a button the failure removes', async () => {
    await openWith(page([letter()]));
    const elsewhere = document.createElement('button');
    document.body.appendChild(elsewhere);
    try {
      elsewhere.focus();
      press('Show payload');
      http.expectOne(WITH_PAYLOAD).flush('down', { status: 502, statusText: 'Bad Gateway' });
      fixture.detectChanges();

      expect(document.activeElement).toBe(elsewhere);
    } finally {
      elsewhere.remove();
    }
  });

  // Here the failure takes every button away, Refresh as well as Show payload: the summary is what is left.
  it('moves the focus to the summary when nothing is left to press', async () => {
    for (const [code, said] of [
      [403, "You are not cleared to see this connection's rejects"],
      [404, 'This connection no longer exists'],
      [501, 'Dead-letter viewing is not enabled on this server'],
    ] as const) {
      for (const [pressed, asked] of [
        ['Refresh', LIST],
        ['Show payload', WITH_PAYLOAD],
      ] as const) {
        render();
        await openWith(page([letter()]));
        button(pressed)!.focus();
        press(pressed);
        http.expectOne(asked).flush({ error: 'no' }, { status: code, statusText: 'No' });
        fixture.detectChanges();

        expect(status()).toBe(said);
        expect(el.querySelector('.actions')).toBeNull();
        expect(document.activeElement).toBe(el.querySelector('summary'));
      }
    }
  });

  it('moves the focus to Refresh when the list that comes back has no rejects to show the payload of', async () => {
    await openWith(page([letter()]));
    button('Show payload')!.focus();
    press('Show payload');
    http.expectOne(WITH_PAYLOAD).flush(page([]));
    fixture.detectChanges();

    expect(button('Show payload')).toBeNull();
    expect(button('Hide payload')).toBeNull();
    expect(document.activeElement).toBe(button('Refresh'));
  });

  // The table goes, and the region or payload block the focus was in goes with it; the page would get the focus.
  it('moves the focus to the summary when the table it was in goes away', async () => {
    // an empty list, a failure, a refusal
    const outcomes: Array<(url: string) => void> = [
      (url) => http.expectOne(url).flush(page([])),
      (url) => http.expectOne(url).flush('down', { status: 502, statusText: 'Bad Gateway' }),
      (url) => http.expectOne(url).flush({ error: 'no' }, { status: 403, statusText: 'No' }),
    ];
    for (const answer of outcomes) {
      for (const inside of ['region', 'payload'] as const) {
        render();
        await openWith(page([letter()]));
        if (inside === 'payload') {
          press('Show payload');
          http.expectOne(WITH_PAYLOAD).flush(page([withPayload()]));
          fixture.detectChanges();
        }
        const target = inside === 'region' ? el.querySelector<HTMLElement>('.scroll')! : pre()[0];
        target.focus();
        expect(document.activeElement).toBe(target);
        // the request is made from a button, which a keyboard user presses and then moves back from
        button('Refresh')!.click();
        target.focus();
        answer(inside === 'payload' ? WITH_PAYLOAD : LIST);
        fixture.detectChanges();

        expect(el.querySelector('table')).toBeNull();
        expect(document.activeElement).toBe(el.querySelector('summary'));
      }
    }
  });

  it('leaves the focus in the table while the table stays', async () => {
    await openWith(page([letter()]));
    const region = el.querySelector<HTMLElement>('.scroll')!;
    button('Refresh')!.click();
    region.focus();
    http.expectOne(LIST).flush(page([letter(), letter({ offset: 43 })]));
    fixture.detectChanges();

    expect(document.activeElement).toBe(el.querySelector('.scroll'));
  });

  it('says that there may be more when the list is as long as the one it asked for', async () => {
    const newest = (n: number) => Array.from({ length: n }, (_, i) => letter({ offset: 100 - i }));
    const lines = () => Array.from(el.querySelectorAll('[role=status] p')).map(text);

    await openWith(page(newest(19)));
    expect(lines()).toEqual(['19 rejects in the last 120 scanned, newest first']);

    press('Refresh');
    http.expectOne(LIST).flush(page(newest(20)));
    fixture.detectChanges();
    expect(rows()).toHaveLength(20);
    expect(lines()).toEqual(['The newest 20 rejects in the last 120 scanned; there may be more']);

    // beside the other thing it can say about the scan
    press('Refresh');
    http.expectOne(LIST).flush(page(newest(20), { partial: true }));
    fixture.detectChanges();
    expect(lines()).toEqual([
      'The newest 20 rejects in the last 120 scanned; there may be more',
      'Partial: the scan stopped at its time limit, so some rejects may be missing',
    ]);

    // and not when it is one reject, or none
    press('Refresh');
    http.expectOne(LIST).flush(page(newest(1)));
    fixture.detectChanges();
    expect(lines()).toEqual(['1 reject in the last 120 scanned, newest first']);
  });

  it('says how far it looked when there are no rejects', async () => {
    await openWith(page([], { scanned: 1200 }));

    expect(status()).toBe('No rejects in the last 1,200 scanned');
    expect(el.querySelector('table')).toBeNull();
    // nothing to show the payload of, but something to refresh
    expect(button('Show payload')).toBeNull();
    expect(button('Refresh')).not.toBeNull();
  });

  it('does not claim to have looked at nothing when the topic is empty', async () => {
    await openWith(page([], { scanned: 0 }));

    expect(status()).toBe('No rejects yet');
    expect(button('Refresh')).not.toBeNull();
  });

  it('notes a partial scan, with rejects and without', async () => {
    await openWith(page([letter()], { partial: true }));
    expect(status()).toContain('1 reject in the last 120 scanned');
    expect(status()).toContain('Partial: the scan stopped at its time limit, so some rejects may be missing');

    press('Refresh');
    http.expectOne(LIST).flush(page([], { partial: true, scanned: 40 }));
    fixture.detectChanges();
    expect(status()).toContain('No rejects in the last 40 scanned');
    expect(status()).toContain('Partial: the scan stopped at its time limit');

    // and nothing of it once a whole scan has been made
    press('Refresh');
    http.expectOne(LIST).flush(page([letter()]));
    fixture.detectChanges();
    expect(status()).not.toContain('Partial');
  });

  it('asks one question at a time, and leaves the focus where it is', async () => {
    await toggle();
    const first = http.expectOne(LIST);
    expect(button('Refresh')!.getAttribute('aria-disabled')).toBe('true');

    // A press while it waits changes nothing, and does not take the focus away from the button either.
    button('Refresh')!.focus();
    press('Refresh');
    noRequest();
    expect(document.activeElement).toBe(button('Refresh'));

    first.flush(page([letter()]));
    fixture.detectChanges();
    expect(button('Refresh')!.getAttribute('aria-disabled')).toBeNull();
    expect(button('Show payload')!.getAttribute('aria-disabled')).toBeNull();

    // The same while the payload comes: pressing again neither hides it nor asks again.
    press('Show payload');
    const payload = http.expectOne(WITH_PAYLOAD);
    expect(button('Hide payload')!.getAttribute('aria-disabled')).toBe('true');
    press('Hide payload');
    noRequest();
    payload.flush(page([withPayload()]));
    fixture.detectChanges();
    expect(pre()).toHaveLength(1);
  });

  // The buttons are refused while a request is out, but a caller that is not a button would not be, and the newer
  // question's answer must not be replaced by the older one's. No button can call `load`, so the test does.
  it('lets only the latest question be answered, whoever asks it', async () => {
    await toggle();
    fixture.componentInstance['load']();
    const [older, newer] = http.match(LIST);

    expect(older.cancelled).toBe(true);
    expect(newer.cancelled).toBe(false);
    newer.flush(page([letter({ offset: 2 })]));
    fixture.detectChanges();
    expect(rows().map((row) => row.querySelector('time')!.getAttribute('title'))).toEqual([
      '2026-10-18T12:00:00Z (partition 0, offset 2)',
    ]);
    expect(button('Refresh')!.getAttribute('aria-disabled')).toBeNull();
  });

  it('stops waiting for an answer when it goes away', async () => {
    await toggle();
    const req = http.expectOne(LIST);
    fixture.destroy();
    expect(req.cancelled).toBe(true);
  });

  it('reads an answer with no list as no rejects', async () => {
    await openWith({ letters: null, scanned: 5, partial: false } as unknown as DeadLetterPage);
    expect(status()).toBe('No rejects in the last 5 scanned');
  });
});
