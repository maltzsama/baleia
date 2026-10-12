// src/routes/(app)/profile/+page.ts
import type { PageLoad } from "./$types";

export const load: PageLoad = async ({ fetch }) => {
  const res = await fetch("/api/v1/me");
  if (!res.ok) return { user: null };
  return { user: await res.json() };
};
