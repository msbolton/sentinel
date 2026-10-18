import { SelectionSubscription } from './selection-subscription';

describe('SelectionSubscription', () => {
  it('subscribes to the selected entity only, moving with the selection', () => {
    const calls: string[] = [];
    const sub = new SelectionSubscription({
      subscribeToEntity: (id) => calls.push(`+${id}`),
      unsubscribeFromEntity: (id) => calls.push(`-${id}`),
    });
    sub.follow('a');
    sub.follow('a'); // same selection: nothing to do
    sub.follow('b');
    sub.follow(null);
    sub.follow(null);
    expect(calls).toEqual(['+a', '-a', '+b', '-b']);
  });
});
