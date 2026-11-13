// Optional Responses compatibility transform, adapted from the MIT-licensed
// AgentRouter-Codex-Filter strip mode. See LICENSE.AgentRouter-Codex-Filter.
// Configure methods: [POST], paths: [/v1/responses, /responses].
// This script does not introduce retries; use the vendor's error_policy.
function onRequest(request) {
  let body;
  try {
    body = JSON.parse(request.body);
  } catch (_) {
    reject(400, "Responses request must contain valid JSON.");
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    reject(400, "Responses request must be a JSON object.");
  }
  if (body.previous_response_id || body.conversation) {
    reject(400, "History filtering requires complete input history instead of previous_response_id or conversation.");
  }
  if (body.include !== undefined && !Array.isArray(body.include)) {
    reject(400, "include must be an array.");
  }
  if (body.input !== undefined && typeof body.input !== "string" && !Array.isArray(body.input)) {
    reject(400, "input must be a string or an array.");
  }
  if (Array.isArray(body.context_management) && body.context_management.some(item => item && item.type === "compaction")) {
    reject(400, "History filtering cannot transfer compaction context. Start a session with a text summary.");
  }

  body.store = false;
  if (Array.isArray(body.include)) {
    body.include = body.include.filter(value => value !== "reasoning.encrypted_content");
  }
  const portableTypes = ["message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output"];
  if (Array.isArray(body.input)) {
    body.input = body.input.filter(item => !item || item.type !== "reasoning").map(item => {
      if (!item || typeof item !== "object") return item;
      if (item.type === "compaction" || item.type === "item_reference" || item.encrypted_content) {
        reject(400, "History filtering cannot transfer encrypted context or item references. Supply complete text history.");
      }
      const message = item.type === undefined && typeof item.role === "string" && item.content !== undefined;
      if (message || portableTypes.includes(item.type)) delete item.id;
      // call_id pairs tools with their results. Nested resource IDs, unknown
      // item types, message content and the current reasoning settings stay.
      return item;
    });
  }
  return JSON.stringify(body);
}
