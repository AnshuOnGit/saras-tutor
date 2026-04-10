/**
 * HintActions — shown after the hint agent delivers a hint.
 * Gives the student options to:
 *   - Submit their attempt (type or photo)
 *   - Try on their own (dismiss)
 *   - Ask for more help (escalate hint level)
 *   - Show full solution
 */
export default function HintActions({ hintLevel, onMoreHelp, onShowSolution, onDismiss }) {
  const isLastHint = hintLevel >= 3;

  return (
    <div className="message hint-actions">
      <p className="hint-actions-prompt">Try solving it and submit your work below (type or photo), or:</p>
      <div className="hint-actions-buttons">
        <button className="btn-hint-dismiss" onClick={onDismiss}>
          ✓ Got it, I solved it
        </button>
        {!isLastHint && (
          <button className="btn-hint-more" onClick={onMoreHelp}>
            💡 Give me another hint
          </button>
        )}
        <button className="btn-hint-solution" onClick={onShowSolution}>
          📝 Show full solution
        </button>
      </div>
      <div className="hint-level-indicator">
        Hint {hintLevel} of 3
        <span className="hint-dots">
          {[1, 2, 3].map((n) => (
            <span key={n} className={`hint-dot ${n <= hintLevel ? "active" : ""}`} />
          ))}
        </span>
      </div>
    </div>
  );
}
