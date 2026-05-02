import { revalidatePath, revalidateTag } from "next/cache";
import { NextRequest, NextResponse } from "next/server";

type Payload = {
  path?: string;
  tenant_id?: string;
};

export async function POST(request: NextRequest) {
  const authHeader = request.headers.get("authorization") || "";
  const expected = process.env.REVALIDATE_TOKEN;
  if (!expected) {
    return NextResponse.json({ error: "missing revalidate token config" }, { status: 500 });
  }

  if (authHeader !== `Bearer ${expected}`) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }

  const payload = (await request.json()) as Payload;
  if (!payload.path && !payload.tenant_id) {
    return NextResponse.json({ error: "path or tenant_id required" }, { status: 400 });
  }

  if (payload.path) {
    revalidatePath(payload.path);
  }
  if (payload.tenant_id) {
    revalidateTag(`tenant:${payload.tenant_id}`);
  }

  return NextResponse.json({ revalidated: true, payload });
}
