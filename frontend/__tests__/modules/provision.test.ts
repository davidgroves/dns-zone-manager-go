import { afterEach, describe, expect, it, vi } from 'vitest';
import { createProvisionMethods } from '../../modules/provision';
import { createInitialState } from '../../state';
import type { AppState } from '../../types';

function makeCtx(overrides: Partial<AppState> = {}) {
  const state = createInitialState({});
  Object.assign(state, overrides);
  const methods = createProvisionMethods(state);
  const toasts: Array<{ message: string; type?: string }> = [];
  const ctx = {
    ...state,
    ...methods,
    toast: (message: string, type?: 'success' | 'error' | 'warning') => {
      toasts.push({ message, type });
    },
    loadZones: vi.fn(async () => {}),
    loadCatalogStatus: vi.fn(async () => {}),
    selectZone: vi.fn(async () => {}),
  };
  return { ctx, toasts };
}

describe('provision', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('loads rndc status', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ enabled: true, host: 'bind', port: 953 }),
      })),
    );
    const { ctx } = makeCtx();
    await ctx.loadRNDCStatus();
    expect(ctx.rndcStatus?.enabled).toBe(true);
    expect(ctx.rndcStatus?.host).toBe('bind');
  });

  it('creates a zone', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'POST') {
          return {
            ok: true,
            json: async () => ({ zone: 'new.example.' }),
          };
        }
        return { ok: true, json: async () => ({ enabled: true }) };
      }),
    );
    const { ctx, toasts } = makeCtx({
      rndcStatus: { enabled: true, catalog_enabled: true },
    });
    ctx.createZoneForm.zone = 'new.example';
    ctx.createZoneForm.addToCatalog = true;
    await ctx.createZone();
    expect(toasts.some((t) => t.message === 'Zone created')).toBe(true);
    expect(ctx.selectZone).toHaveBeenCalled();
  });
});
