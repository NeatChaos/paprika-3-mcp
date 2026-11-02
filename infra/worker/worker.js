// Forwards every request to the Cloud Run origin, keeping method, path, query, headers and body.
// The response body is returned as a stream, so SSE from MCP Streamable HTTP is not buffered.
export default {
  async fetch(request, env) {
    const incoming = new URL(request.url);
    const target = new URL(env.ORIGIN);
    target.pathname = incoming.pathname;
    target.search = incoming.search;
    return fetch(new Request(target, request), { redirect: "manual" });
  },
};
