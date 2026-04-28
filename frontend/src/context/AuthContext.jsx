import { createContext, useContext, useState, useEffect, useCallback } from "react";

const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [user, setUser] = useState(null);
  const [loading, setLoading] = useState(true);

  // Check auth status on mount and after callback
  const checkAuth = useCallback(async () => {
    try {
      const res = await fetch("/api/v1/users/me", { credentials: "include" });
      if (res.ok) {
        const data = await res.json();
        setUser(data.user);
      } else if (res.status === 401) {
        // Try refreshing the token
        const refreshRes = await fetch("/api/v1/auth/refresh", {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        });
        if (refreshRes.ok) {
          // Retry profile
          const retryRes = await fetch("/api/v1/users/me", { credentials: "include" });
          if (retryRes.ok) {
            const data = await retryRes.json();
            setUser(data.user);
          } else {
            setUser(null);
          }
        } else {
          setUser(null);
        }
      }
    } catch {
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Handle OAuth callback — the server redirects to /auth/callback#access_token=...
    if (window.location.pathname === "/auth/callback") {
      // Tokens are set as HttpOnly cookies by the server, so we just need to check auth
      checkAuth();
      // Clean the URL
      window.history.replaceState({}, "", "/");
      return;
    }

    // Handle auth errors
    if (window.location.pathname === "/auth/error") {
      const params = new URLSearchParams(window.location.search);
      const error = params.get("message") || "Authentication failed";
      console.error("Auth error:", error);
      setLoading(false);
      window.history.replaceState({}, "", "/");
      return;
    }

    checkAuth();
  }, [checkAuth]);

  const login = () => {
    window.location.href = "/api/v1/auth/google";
  };

  const logout = async () => {
    try {
      await fetch("/api/v1/auth/logout", {
        method: "POST",
        credentials: "include",
      });
    } catch {
      // ignore
    }
    setUser(null);
  };

  return (
    <AuthContext.Provider value={{ user, loading, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
