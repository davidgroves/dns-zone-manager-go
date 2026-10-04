import {
  API_BASE,
  api,
  formatDNSErrorWithRequestID,
  requestIDFromResponse,
} from '../api/client';
import type { AppState, CreateZoneForm, RNDCStatus } from '../types';

type ProvisionContext = AppState & {
  toast: (message: string, type?: 'success' | 'error' | 'warning') => void;
  loadZones: (cursor?: string | null, resetHistory?: boolean) => Promise<void>;
  loadCatalogStatus: () => Promise<void>;
  selectZone: (zone: string) => Promise<void>;
};

function blankCreateZoneForm(status: RNDCStatus | null): CreateZoneForm {
  const d = status?.defaults;
  return {
    zone: '',
    primaryNs: d?.primary_ns ?? '',
    adminEmail: d?.admin_email ?? '',
    nameservers: (d?.nameservers ?? []).join('\n'),
    addToCatalog: status?.catalog_enabled !== false,
    schedule: false,
    scheduledLocal: '',
  };
}

function localDatetimeToIso(value: string): string | null {
  if (!value) return null;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return null;
  return d.toISOString();
}

export function createProvisionMethods(_state: AppState) {
  return {
    async loadRNDCStatus(this: ProvisionContext) {
      try {
        const response = await api(`${API_BASE}/rndc/status`, this.apiKey);
        if (response.ok) {
          this.rndcStatus = (await response.json()) as RNDCStatus;
        } else {
          this.rndcStatus = { enabled: false };
        }
      } catch {
        this.rndcStatus = { enabled: false };
      }
    },

    openCreateZone(this: ProvisionContext) {
      this.createZoneForm = blankCreateZoneForm(this.rndcStatus);
      this.showCreateZone = true;
    },

    async createZone(this: ProvisionContext) {
      const zone = this.createZoneForm.zone.trim();
      if (!zone) return;
      this.creatingZone = true;
      try {
        const nameservers = this.createZoneForm.nameservers
          .split(/[\n,]+/)
          .map((s) => s.trim())
          .filter(Boolean);
        const body: Record<string, unknown> = {
          zone,
          catalog: this.createZoneForm.addToCatalog,
        };
        if (this.createZoneForm.primaryNs.trim()) {
          body.primary_ns = this.createZoneForm.primaryNs.trim();
        }
        if (this.createZoneForm.adminEmail.trim()) {
          body.admin_email = this.createZoneForm.adminEmail.trim();
        }
        if (nameservers.length) {
          body.nameservers = nameservers;
        }
        if (this.createZoneForm.schedule) {
          const at = localDatetimeToIso(this.createZoneForm.scheduledLocal);
          if (!at) {
            this.toast('Pick a valid schedule time', 'error');
            return;
          }
          body.scheduled_at = at;
        }
        const response = await api(`${API_BASE}/zones`, this.apiKey, {
          method: 'POST',
          body: JSON.stringify(body),
        });
        if (!response.ok) {
          const err = await response.json().catch(() => ({}));
          this.toast(
            `Failed to create zone: ${formatDNSErrorWithRequestID(err, requestIDFromResponse(response))}`,
            'error',
          );
          return;
        }
        const data = (await response.json()) as { zone?: string; id?: string };
        this.showCreateZone = false;
        if (body.scheduled_at) {
          this.toast('Zone create scheduled', 'success');
        } else {
          this.toast('Zone created', 'success');
          await this.loadZones();
          await this.loadCatalogStatus();
          if (data.zone) {
            await this.selectZone(data.zone);
          }
        }
      } catch (e) {
        this.toast(`Failed to create zone: ${(e as Error).message}`, 'error');
      } finally {
        this.creatingZone = false;
      }
    },

    async deleteSelectedZone(this: ProvisionContext) {
      if (!this.selectedZone) return;
      this.deletingZone = true;
      try {
        const response = await api(
          `${API_BASE}/zones/${encodeURIComponent(this.selectedZone)}`,
          this.apiKey,
          { method: 'DELETE' },
        );
        if (!response.ok) {
          const err = await response.json().catch(() => ({}));
          this.toast(
            `Failed to delete zone: ${formatDNSErrorWithRequestID(err, requestIDFromResponse(response))}`,
            'error',
          );
          return;
        }
        this.showDeleteZoneConfirm = false;
        this.toast('Zone deleted', 'success');
        this.selectedZone = null;
        await this.loadZones();
        await this.loadCatalogStatus();
      } catch (e) {
        this.toast(`Failed to delete zone: ${(e as Error).message}`, 'error');
      } finally {
        this.deletingZone = false;
      }
    },
  };
}
