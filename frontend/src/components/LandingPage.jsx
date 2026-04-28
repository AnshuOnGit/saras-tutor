import { useAuth } from "../context/AuthContext";
import logoSvg from "../assets/logo.svg";

export default function LandingPage() {
  const { login } = useAuth();

  return (
    <div className="landing">
      {/* Nav */}
      <nav className="landing-nav">
        <div className="landing-nav-brand">
          <img src={logoSvg} alt="Saras" className="landing-logo" />
          <span className="landing-brand-text">Saras Studio</span>
        </div>
        <button className="btn btn-google-sign-in" onClick={login}>
          <GoogleIcon />
          Sign in with Google
        </button>
      </nav>

      {/* Hero */}
      <section className="landing-hero">
        <div className="landing-hero-content">
          <h1 className="landing-title">
            Your AI-Powered<br />
            <span className="landing-gradient-text">JEE & NEET Companion</span>
          </h1>
          <p className="landing-subtitle">
            Upload question papers, get step-by-step solutions, hints, and
            evaluate your attempts — all powered by cutting-edge AI models.
          </p>
          <button className="btn btn-hero-cta" onClick={login}>
            <GoogleIcon />
            Get Started with Google
          </button>
        </div>
        <div className="landing-hero-visual">
          <img src={logoSvg} alt="Saras" className="landing-hero-logo" />
        </div>
      </section>

      {/* About Saras */}
      <section className="landing-about">
        <h2 className="landing-section-title">Who is Saras?</h2>
        <div className="landing-about-grid">
          <article className="landing-card">
            <div className="landing-card-icon">🪷</div>
            <h3>Goddess of Knowledge</h3>
            <p>
              Saraswati — often called <em>Saras</em> — is the Hindu goddess
              of knowledge, music, art, and learning. She is depicted holding a
              veena and sacred scriptures, seated on a white lotus, embodying
              wisdom and creative energy.
            </p>
          </article>
          <article className="landing-card">
            <div className="landing-card-icon">📿</div>
            <h3>Eternal Teacher</h3>
            <p>
              In the Vedic tradition, Saraswati is the one who &ldquo;flows&rdquo;
              — like a river of intellect. Students have invoked her blessings
              before exams for millennia. She represents the idea that knowledge
              is the highest form of liberation.
            </p>
          </article>
          <article className="landing-card">
            <div className="landing-card-icon">🧠</div>
            <h3>AI Meets Tradition</h3>
            <p>
              <strong>Saras Studio</strong> carries forward this spirit. We pair
              the world&rsquo;s most advanced vision and reasoning AI models
              with a beautiful workspace designed to help you truly
              <em> understand </em> every problem — not just get an answer.
            </p>
          </article>
        </div>
      </section>

      {/* Features */}
      <section className="landing-features">
        <h2 className="landing-section-title">How It Works</h2>
        <div className="landing-steps">
          <div className="landing-step">
            <div className="landing-step-num">1</div>
            <h3>Upload</h3>
            <p>Snap a photo of any JEE or NEET question. Our OCR models extract the text with full LaTeX math support.</p>
          </div>
          <div className="landing-step-arrow">→</div>
          <div className="landing-step">
            <div className="landing-step-num">2</div>
            <h3>Workspace</h3>
            <p>Drag questions and attempts into the workspace. Label them and arrange your problem-solving session.</p>
          </div>
          <div className="landing-step-arrow">→</div>
          <div className="landing-step">
            <div className="landing-step-num">3</div>
            <h3>Solve</h3>
            <p>Get detailed solutions, targeted hints, or have your attempt evaluated — then ask follow-up questions.</p>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="landing-footer">
        <img src={logoSvg} alt="Saras" className="landing-footer-logo" />
        <p>Built with 🪷 for students who dream big.</p>
      </footer>
    </div>
  );
}

function GoogleIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 48 48" style={{ flexShrink: 0 }}>
      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
    </svg>
  );
}
