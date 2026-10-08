import { afterEach, describe, expect, it, vi } from 'vitest';
import { createZoneMethods, pageRelationForName } from '../../modules/zones';
import { createInitialState } from '../../state';
import type { RRset } from '../../types';

function rr(name: string, type = 'A'): RRset {
  return {
    name,
    type,
    rdclass: 'IN',
    ttl: 300,
    records: ['192.0.2.1'],
  };
}

describe('pageRelationForName', () => {
  it('returns empty for no records', () => {
    expect(pageRelationForName([], 'www.example.com.')).toBe('empty');
  });

  it('returns found when name is on the page', () => {
    expect(
      pageRelationForName(
        [rr('aaa.example.com.'), rr('www.example.com.')],
        'www.example.com.',
      ),
    ).toBe('found');
  });

  it('normalizes missing trailing dots', () => {
    expect(
      pageRelationForName([rr('www.example.com.')], 'www.example.com'),
    ).toBe('found');
  });

  it('returns before when target is before the page', () => {
    expect(
      pageRelationForName(
        [rr('m.example.com.'), rr('z.example.com.')],
        'a.example.com.',
      ),
    ).toBe('before');
  });

  it('returns after when target is after the page', () => {
    expect(
      pageRelationForName(
        [rr('a.example.com.'), rr('b.example.com.')],
        'z.example.com.',
      ),
    ).toBe('after');
  });

  it('returns absent when target is between bounds but missing', () => {
    expect(
      pageRelationForName(
        [rr('a.example.com.'), rr('z.example.com.')],
        'm.example.com.',
      ),
    ).toBe('absent');
  });
});

describe('goToZoneName', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('selects the zone, sets focus, and scrolls to the name', async () => {
    vi.stubGlobal('window', {
      history: { replaceState: vi.fn() },
      location: { pathname: '/', search: '' },
    });

    const state = createInitialState({});
    const methods = createZoneMethods(state);
    const selectZone = vi.fn(async () => {
      state.selectedZone = 'example.com.';
      state.records = [rr('www.example.com.'), rr('mail.example.com.')];
      state.totalRecords = 2;
      state.pageSize = 25;
    });
    const focusNameInZone = vi.fn(async () => true);
    const ctx = {
      ...state,
      ...methods,
      selectZone,
      focusNameInZone,
      toast: vi.fn(),
      loadRecords: vi.fn(async () => {}),
      clearSearch: vi.fn(),
      loadZones: vi.fn(async () => {}),
      loadCatalogStatus: vi.fn(async () => {}),
      loadZonesFirstPage: vi.fn(async () => {}),
      loadZonesWithOffset: vi.fn(async () => {}),
      goToRecordPage: vi.fn(async () => {}),
      scrollToFocusName: vi.fn(),
      connectZoneLive: vi.fn(),
      disconnectZoneLive: vi.fn(),
    };

    await ctx.goToZoneName('example.com.', 'www.example.com.');

    expect(selectZone).toHaveBeenCalledWith('example.com.');
    expect(ctx.focusName).toBe('www.example.com.');
    expect(focusNameInZone).toHaveBeenCalledWith('www.example.com.');
  });

  it('builds a zone+focus href', () => {
    const state = createInitialState({});
    const methods = createZoneMethods(state);
    const href = methods.zoneNameHref.call(
      state as never,
      'example.com.',
      'www.example.com',
    );
    expect(href).toBe('?zone=example.com.&focus=www.example.com.');
  });
});
