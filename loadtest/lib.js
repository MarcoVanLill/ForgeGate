import http from 'k6/http';

export const baseURL = __ENV.FORGEGATE_URL || 'http://localhost:8080';
export const apiKey = __ENV.FORGEGATE_API_KEY || 'fg_demo_local_only';
export const headers = { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' };

export function response(model = 'fake/echo', input = 'k6 request', stream = false) {
  return http.post(`${baseURL}/v1/responses`, JSON.stringify({ model, input, stream }), { headers });
}
