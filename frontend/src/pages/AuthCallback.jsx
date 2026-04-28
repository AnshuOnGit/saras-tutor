import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../context/AuthContext";

export default function AuthCallback() {
  const { user, loading, checkAuth } = useAuth();
  const navigate = useNavigate();

  useEffect(() => {
    checkAuth();
  }, [checkAuth]);

  useEffect(() => {
    if (!loading) {
      // Auth check finished — redirect to home regardless
      navigate("/", { replace: true });
    }
  }, [loading, user, navigate]);

  return (
    <div className="loading-screen">
      <div className="spinner" />
      <p style={{ color: "var(--text-secondary)", marginTop: 16, fontSize: 14 }}>
        Logging you in…
      </p>
    </div>
  );
}
