import { Signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';
import { AuthService } from '../../core/services/auth.service';

/** Whether the current user is a demo guest, kept up to date as the profile changes. Call in an injection context. */
export function guestSignal(auth: AuthService): Signal<boolean> {
  return toSignal(auth.userProfile$.pipe(map(() => auth.isGuest())), { initialValue: auth.isGuest() }) as Signal<boolean>;
}
