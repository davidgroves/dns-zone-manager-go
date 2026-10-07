import { API_BASE, api } from '../api/client';
import type { AppState, RouteParams } from '../types';
import { syncUrlFromState } from './router';

const LEGACY_API_KEY_STORAGE_KEY = 'dns_zone_manager_api_key';

// Method context type - the full app state with methods (uses any to avoid circular refs)
type MethodContext = AppState & {
  loadZones: () => Promise<void>;
  validateAndSetAuth: () => Promise<void>;
  trackLogin: (authType: string) => Promise<void>;
  trackLogout: () => Promise<void>;
  navigateToRoute: (route: RouteParams) => Promise<void>;
  updateUrlFromState: () => void;
  disconnectZoneLive: () => void;
};

/**
 * Drop any API key previously written to Web Storage (CodeQL js/clear-text-storage-of-sensitive-data).
 * Stay-logged-in uses an HttpOnly session cookie issued by POST /v1/auth/session.
 */
export function purgeLegacyApiKeyStorage(): void {
  try {
    sessionStorage.removeItem(LEGACY_API_KEY_STORAGE_KEY);
    localStorage.removeItem(LEGACY_API_KEY_STORAGE_KEY);
  } catch {
    // Ignore storage access errors (private mode / blocked).
  }
}

/**
 * Authentication module.
 * NOTE: Methods use `this` (the Alpine proxy) for state changes to trigger reactivity.
 */
export function createAuthMethods(_state: AppState) {
  return {
    /**
     * Login with API key.
     */
    async loginApiKey(this: MethodContext) {
      if (!this.apiKeyInput) {
        this.loginError = 'Please enter an API key';
        return;
      }

      this.apiKey = this.apiKeyInput;
      await this.validateAndSetAuth();
    },

    /**
     * Validate API key and set authentication state.
     */
    async validateAndSetAuth(this: MethodContext) {
      try {
        const response = await api(`${API_BASE}/auth/validate`, this.apiKey);

        if (response.ok) {
          purgeLegacyApiKeyStorage();
          if (this.apiKey) {
            const sessionRes = await api(
              `${API_BASE}/auth/session`,
              this.apiKey,
              {
                method: 'POST',
                body: JSON.stringify({ remember: this.rememberApiKey }),
              },
            );
            if (sessionRes.ok) {
              // Cookie carries the session; do not keep the API key in JS.
              this.apiKey = null;
              this.apiKeyInput = '';
            }
          }
          this.authenticated = true;
          this.loginError = '';
          this.trackLogin('api_key');

          // Navigate to intended route if one was stored, otherwise load zones
          if (this.intendedRoute) {
            await this.navigateToRoute(this.intendedRoute);
          } else {
            await this.loadZones();
            this.updateUrlFromState();
          }
        } else if (response.status === 401) {
          this.loginError = 'Invalid API key';
          this.apiKey = null;
          purgeLegacyApiKeyStorage();
        } else if (response.status === 403) {
          this.loginError = 'Access denied';
          this.apiKey = null;
          purgeLegacyApiKeyStorage();
        } else {
          const errorText = await response.text();
          this.loginError = `Authentication failed: ${errorText || response.statusText}`;
          this.apiKey = null;
          purgeLegacyApiKeyStorage();
        }
      } catch (e) {
        this.loginError = `Connection error: ${(e as Error).message}`;
        this.apiKey = null;
        purgeLegacyApiKeyStorage();
      }
    },

    /**
     * Restore a browser session from the HttpOnly cookie (no API key in JS).
     */
    async restoreSession(this: MethodContext): Promise<boolean> {
      purgeLegacyApiKeyStorage();
      try {
        const response = await api(`${API_BASE}/auth/validate`, null);
        if (!response.ok) {
          return false;
        }
        this.authenticated = true;
        this.loginError = '';
        if (this.intendedRoute) {
          await this.navigateToRoute(this.intendedRoute);
        } else {
          await this.loadZones();
          this.updateUrlFromState();
        }
        return true;
      } catch {
        return false;
      }
    },

    /**
     * Track login event for metrics.
     */
    async trackLogin(this: MethodContext, authType: string) {
      try {
        await api(`${API_BASE}/auth/login`, this.apiKey, {
          method: 'POST',
          body: JSON.stringify({ auth_type: authType }),
        });
      } catch (e) {
        console.warn('Failed to track login:', e);
      }
    },

    /**
     * Track logout event for metrics.
     */
    async trackLogout(this: MethodContext) {
      try {
        await api(`${API_BASE}/auth/logout`, this.apiKey, { method: 'POST' });
      } catch (e) {
        console.warn('Failed to track logout:', e);
      }
    },

    /**
     * Logout and clear state.
     */
    async logout(this: MethodContext) {
      this.disconnectZoneLive();
      const wasProxyUser = Boolean(this.proxyAuthEnabled && this.currentUser);
      await this.trackLogout();
      this.authenticated = false;
      this.apiKey = null;
      this.apiKeyInput = '';
      this.currentUser = null;
      purgeLegacyApiKeyStorage();
      this.zones = [];
      this.selectedZone = null;
      this.records = [];
      this.catalogStatus = null;
      this.catalogZones = new Set();
      this.intendedRoute = null;
      // Clear URL params on logout
      syncUrlFromState(this);
      // In proxy-auth mode the session is owned by the front proxy / IdP.
      // Prefer proxyLogoutUrl (IdP front-channel logout → oauth2-proxy
      // sign_out). Falling back to /oauth2/sign_out alone leaves the IdP
      // session and silent-SSO will log the user straight back in.
      if (wasProxyUser) {
        window.location.href = this.proxyLogoutUrl || '/oauth2/sign_out';
      }
    },
  };
}
