import { pushAlert } from './alert-ticker';
import { Alert, AlertSeverity, AlertType } from './models/alert.model';

function alert(id: string, over: Partial<Alert> = {}): Alert {
  return {
    id,
    title: `Alert ${id}`,
    message: '',
    severity: AlertSeverity.MEDIUM,
    alertType: AlertType.UNSPECIFIED,
    acknowledged: false,
    metadata: {},
    createdAt: '2026-11-01T14:00:00Z',
    updatedAt: '2026-11-01T14:00:00Z',
    ...over,
  };
}

describe('pushAlert', () => {
  const [a1, a2, a3, a4, a5, a6] = ['a1', 'a2', 'a3', 'a4', 'a5', 'a6'].map((id) => alert(id));

  it('inserts newest first and caps at five', () => {
    const list = [a1, a2, a3, a4, a5].reduce<Alert[]>((l, a) => pushAlert(l, a), []);
    expect(list.map((a) => a.id)).toEqual(['a5', 'a4', 'a3', 'a2', 'a1']);
    expect(pushAlert(list, a6).map((a) => a.id)).toEqual(['a6', 'a5', 'a4', 'a3', 'a2']);
  });

  it('replaces an alert that arrives again with the same id', () => {
    const updated = { ...a2, title: 'Updated' };
    const out = pushAlert([a1, a2, a3], updated);
    expect(out).toHaveLength(3);
    expect(out[0]).toBe(updated);
    expect(out.map((a) => a.id)).toEqual(['a2', 'a1', 'a3']);
  });

  it('removes an alert once it is acknowledged', () => {
    expect(pushAlert([a1, a2], { ...a1, acknowledged: true }).map((a) => a.id)).toEqual(['a2']);
  });

  it('ignores an acknowledged alert that is not in the list', () => {
    expect(pushAlert([a1], { ...a2, acknowledged: true }).map((a) => a.id)).toEqual(['a1']);
  });

  it('honours a custom max and never mutates its input', () => {
    const list = [a1, a2];
    const snapshot = [...list];
    expect(pushAlert(list, a3, 2).map((a) => a.id)).toEqual(['a3', 'a1']);
    pushAlert(list, { ...a1, acknowledged: true });
    expect(list).toEqual(snapshot);
  });
});
