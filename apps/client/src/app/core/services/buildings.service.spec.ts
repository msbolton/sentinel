import { TestBed } from '@angular/core/testing';
import { BuildingsService } from './buildings.service';
import * as liteMode from '../../features/map/lite-mode';

describe('BuildingsService', () => {
  afterEach(() => jest.restoreAllMocks());

  it.each([true, false])('3D buildings start off (startsLite=%s) until the user turns them on', (lite) => {
    jest.spyOn(liteMode, 'startsLite').mockReturnValue(lite);
    TestBed.resetTestingModule();
    const svc = TestBed.inject(BuildingsService);
    svc.init({}, { Ion: {} });
    expect(svc.buildings3dEnabled()).toBe(false);
  });
});
