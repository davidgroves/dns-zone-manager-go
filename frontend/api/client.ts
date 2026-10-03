/**
 * API client for DNS API requests.
 * All methods use the apiKey from state for authentication.
 */

/**
 * Base path for all versioned API endpoints.
 * Update this when bumping API version.
 */
export const API_BASE = '/v1';

export interface ApiOptions extends RequestInit {
  headers?: Record<string, string>;
}

/**
 * Make an API request with authentication.
 */
export function api(
  path: string,
  apiKey: string | null,
  options: ApiOptions = {},
): Promise<Response> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...options.headers,
  };

  if (apiKey) {
    headers['X-API-Key'] = apiKey;
  }

  return fetch(path, {
    ...options,
    credentials: 'include',
    headers,
  });
}

/**
 * Read X-Request-ID from a Response, if present.
 */
export function requestIDFromResponse(res: Response): string | null {
  try {
    return res.headers?.get?.('X-Request-ID') ?? null;
  } catch {
    return null;
  }
}

/**
 * Parse API error response into a user-friendly message.
 *
 * Prefers RFC 9457 problem+json fields, with FastAPI nested-detail fallback.
 */
export function formatDNSError(err: unknown): string {
  if (err && typeof err === 'object') {
    const errObj = err as Record<string, unknown>;

    // RFC 9457 / Go problem+json: top-level rcode_description + rcode
    if (errObj.rcode_description != null && errObj.rcode_description !== '') {
      return `${errObj.rcode_description} (${errObj.rcode})`;
    }

    // Top-level string detail
    if (typeof errObj.detail === 'string') {
      return errObj.detail;
    }

    // Top-level errors[] (validation / multi-error)
    if (Array.isArray(errObj.errors)) {
      const msgs = joinErrorMessages(errObj.errors);
      if (msgs) {
        return msgs;
      }
    }

    // Nested FastAPI-compat detail object
    const detail = errObj.detail;
    if (detail && typeof detail === 'object' && !Array.isArray(detail)) {
      const detailObj = detail as Record<string, unknown>;
      if (
        detailObj.rcode_description != null &&
        detailObj.rcode_description !== ''
      ) {
        return `${detailObj.rcode_description} (${detailObj.rcode})`;
      }
      if (detailObj.message != null && detailObj.message !== '') {
        return String(detailObj.message);
      }
    }

    if (errObj.message != null && errObj.message !== '') {
      return String(errObj.message);
    }
  }

  if (typeof err === 'string') {
    return err;
  }

  return 'Unknown error';
}

/**
 * Like formatDNSError, but appends ` (request_id)` when present.
 */
export function formatDNSErrorWithRequestID(
  err: unknown,
  requestId?: string | null,
): string {
  const msg = formatDNSError(err);
  if (requestId) {
    return `${msg} (${requestId})`;
  }
  return msg;
}

function joinErrorMessages(errors: unknown[]): string | null {
  const msgs = errors
    .map((e) => {
      if (e && typeof e === 'object') {
        const o = e as Record<string, unknown>;
        if (o.message != null && o.message !== '') {
          return String(o.message);
        }
        if (o.msg != null && o.msg !== '') {
          return String(o.msg);
        }
      }
      return null;
    })
    .filter((m): m is string => m != null);
  return msgs.length > 0 ? msgs.join('; ') : null;
}
