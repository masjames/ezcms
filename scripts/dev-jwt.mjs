import crypto from "node:crypto";

const tenantId = process.argv[2] || "acme";
const secret = process.env.JWT_SECRET || "local-dev-secret";
const now = Math.floor(Date.now() / 1000);

const header = { alg: "HS256", typ: "JWT" };
const payload = {
  sub: "local-editor",
  tenant_id: tenantId,
  iat: now,
  exp: now + 60 * 60 * 8,
};

const encode = (value) =>
  Buffer.from(JSON.stringify(value))
    .toString("base64url");

const signingInput = `${encode(header)}.${encode(payload)}`;
const signature = crypto
  .createHmac("sha256", secret)
  .update(signingInput)
  .digest("base64url");

process.stdout.write(`${signingInput}.${signature}\n`);

