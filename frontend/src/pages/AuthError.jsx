import { useEffect } from "react";

export default function AuthError() {
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const message = params.get("message") || "Authentication failed";
    console.error("Auth error:", message);
  }, []);

  return (
    <div className="loading-screen" style={{ gap: 16 }}>
      <div style={{ fontSize: 48 }}>⚠️</div>
      <h2 style={{ color: "var(--text-primary)", fontSize: 20 }}>Authentication Failed</h2>
      <p style={{ color: "var(--text-secondary)", fontSize: 14 }}>
        {new URLSearchParams(window.location.search).get("message") || "Something went wrong. Please try again."}
      </p>
      <a
        href="/"
        style={{
          marginTop: 16,
          padding: "10px 24px",
          background: "var(--accent-solver)",
          color: "#fff",
          borderRadius: "var(--radius-md)",
          textDecoration: "none",
          fontWeight: 600,
          fontSize: 14,
        }}
      >
        Back to Home
      </a>
    </div>
  );
}
