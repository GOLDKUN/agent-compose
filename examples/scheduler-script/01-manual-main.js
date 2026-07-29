function normalizePayload(payload) {
  return payload ?? {};
}

function main(payload) {
  const input = normalizePayload(payload);
  const result = {
    ok: true,
    mode: "manual",
    receivedAt: new Date().toISOString(),
    input: input,
  };

  scheduler.log("manual scheduler executed", result);
  return result;
}
