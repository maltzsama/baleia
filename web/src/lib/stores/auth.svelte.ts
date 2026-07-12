// src/lib/stores/auth.svelte.ts

export type AuthUser = {
  id: string;
  name: string;
  email: string;
  role: string;
  auth_provider: string;
  created_at: string;
};

function createAuthStore() {
  let user = $state<AuthUser | null>(null);
  let loading = $state(true);

  function initials(): string {
    if (!user) return "?";
    return user.name
      .split(" ")
      .map((p) => p[0])
      .slice(0, 2)
      .join("")
      .toUpperCase();
  }

  async function load() {
    loading = true;
    try {
      const token = localStorage.getItem('token');
      const res = await fetch("/api/v1/auth/me", {
        headers: token ? { Authorization: `Bearer ${token}` } : {}
      });
      if (res.ok) user = await res.json();
      else user = null;
    } finally {
      loading = false;
    }
  }

  function set(u: AuthUser | null) {
    user = u;
  }

  function clear() {
    user = null;
  }

  return {
    get user() {
      return user;
    },
    get loading() {
      return loading;
    },
    get initials() {
      return initials();
    },
    load,
    set,
    clear,
  };
}

export const authStore = createAuthStore();
