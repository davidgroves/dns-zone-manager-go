import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  api,
  formatDNSError,
  formatDNSErrorWithRequestID,
  requestIDFromResponse,
} from '../../api/client';

describe('api', () => {
  const mockFetch = vi.fn();

  beforeEach(() => {
    vi.stubGlobal('fetch', mockFetch);
    mockFetch.mockClear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('should make a request with default headers', async () => {
    mockFetch.mockResolvedValue(new Response('{}', { status: 200 }));

    await api('/zones', null);

    expect(mockFetch).toHaveBeenCalledWith('/zones', {
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
      },
    });
  });

  it('should include API key header when provided', async () => {
    mockFetch.mockResolvedValue(new Response('{}', { status: 200 }));

    await api('/zones', 'test-api-key');

    expect(mockFetch).toHaveBeenCalledWith('/zones', {
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': 'test-api-key',
      },
    });
  });

  it('should not include API key header when null', async () => {
    mockFetch.mockResolvedValue(new Response('{}', { status: 200 }));

    await api('/zones', null);

    const callHeaders = mockFetch.mock.calls[0][1].headers;
    expect(callHeaders['X-API-Key']).toBeUndefined();
  });

  it('should pass through additional options', async () => {
    mockFetch.mockResolvedValue(new Response('{}', { status: 200 }));

    await api('/zones', 'key', {
      method: 'POST',
      body: JSON.stringify({ name: 'test' }),
    });

    expect(mockFetch).toHaveBeenCalledWith('/zones', {
      method: 'POST',
      body: JSON.stringify({ name: 'test' }),
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': 'key',
      },
    });
  });

  it('should merge custom headers with defaults', async () => {
    mockFetch.mockResolvedValue(new Response('{}', { status: 200 }));

    await api('/zones', 'key', {
      headers: {
        'X-Custom': 'value',
      },
    });

    expect(mockFetch).toHaveBeenCalledWith('/zones', {
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': 'key',
        'X-Custom': 'value',
      },
    });
  });

  it('should return the Response object', async () => {
    const mockResponse = new Response('{"data": "test"}', { status: 200 });
    mockFetch.mockResolvedValue(mockResponse);

    const response = await api('/zones', 'key');

    expect(response).toBe(mockResponse);
  });
});

describe('requestIDFromResponse', () => {
  it('reads X-Request-ID', () => {
    const res = new Response('{}', {
      headers: { 'X-Request-ID': 'req-abc' },
    });
    expect(requestIDFromResponse(res)).toBe('req-abc');
  });

  it('returns null when missing', () => {
    expect(requestIDFromResponse(new Response('{}'))).toBeNull();
  });
});

describe('formatDNSError', () => {
  it('prefers top-level rcode_description + rcode (problem+json)', () => {
    const error = {
      type: 'about:blank',
      title: 'DNS error',
      status: 502,
      rcode: 'REFUSED',
      rcode_description: 'Query refused by server',
      detail: 'should not win',
    };
    expect(formatDNSError(error)).toBe('Query refused by server (REFUSED)');
  });

  it('uses top-level string detail', () => {
    const error = {
      detail: 'Invalid request',
    };
    expect(formatDNSError(error)).toBe('Invalid request');
  });

  it('joins top-level errors[].message', () => {
    const error = {
      errors: [{ message: 'name is required' }, { message: 'type is invalid' }],
    };
    expect(formatDNSError(error)).toBe('name is required; type is invalid');
  });

  it('joins top-level errors[].msg', () => {
    const error = {
      errors: [{ msg: 'field required' }, { msg: 'too short' }],
    };
    expect(formatDNSError(error)).toBe('field required; too short');
  });

  it('surfaces CNAME exclusivity conflict detail', () => {
    const error = {
      detail:
        'Cannot add CNAME at host.example.com.: name already has A. Delete those records first (or include deletes in the same atomic update).',
    };
    expect(formatDNSError(error)).toContain('already has A');
  });

  // Nested FastAPI-compat detail (legacy)
  it('extracts rcode_description from nested detail', () => {
    const error = {
      detail: {
        rcode: 'REFUSED',
        rcode_description: 'Query refused by server',
      },
    };
    expect(formatDNSError(error)).toBe('Query refused by server (REFUSED)');
  });

  it('extracts message from nested detail', () => {
    const error = {
      detail: {
        message: 'Zone not found',
      },
    };
    expect(formatDNSError(error)).toBe('Zone not found');
  });

  it('prioritizes nested rcode_description over nested message', () => {
    const error = {
      detail: {
        rcode: 'NXDOMAIN',
        rcode_description: 'Domain does not exist',
        message: 'Generic message',
      },
    };
    expect(formatDNSError(error)).toBe('Domain does not exist (NXDOMAIN)');
  });

  it('falls back to top-level message', () => {
    const error = {
      message: 'Connection failed',
    };
    expect(formatDNSError(error)).toBe('Connection failed');
  });

  it('handles string errors', () => {
    expect(formatDNSError('Simple error')).toBe('Simple error');
  });

  it('returns "Unknown error" for unhandled types', () => {
    expect(formatDNSError(null)).toBe('Unknown error');
    expect(formatDNSError(undefined)).toBe('Unknown error');
    expect(formatDNSError(123)).toBe('Unknown error');
    expect(formatDNSError({})).toBe('Unknown error');
  });

  it('returns Unknown for FastAPI validation detail arrays', () => {
    const error = {
      detail: [
        {
          loc: ['body', 'name'],
          msg: 'field required',
          type: 'value_error.missing',
        },
      ],
    };
    expect(formatDNSError(error)).toBe('Unknown error');
  });
});

describe('formatDNSErrorWithRequestID', () => {
  it('appends request id when present', () => {
    expect(formatDNSErrorWithRequestID({ detail: 'boom' }, 'req-1')).toBe(
      'boom (req-1)',
    );
  });

  it('omits request id when absent', () => {
    expect(formatDNSErrorWithRequestID({ detail: 'boom' }, null)).toBe('boom');
    expect(formatDNSErrorWithRequestID({ detail: 'boom' })).toBe('boom');
  });
});
