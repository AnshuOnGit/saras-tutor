/**
 * HintActions — shown after a hint is delivered. Lets the student:
 *   - Submit an attempt (type or photo via the InputBar)
 *   - Dismiss ("I solved it")
 *   - Show the full solution
 *
 * To get a different hint the student picks an alternative HintGenerator
 * model from the picker that the backend emits alongside every hint.
 */
export default function HintActions({ onShowSolution, onDismiss }) {
  return (
    <div className="message hint-actions">
      <p className="hint-actions-prompt">Try solving it and submit your work below (type or photo), or:</p>
      <div className="hint-actions-buttons">
        <button className="btn-hint-dismiss" onClick={onDismiss}>
          ✓ Got it, I solved it
        </button>
        <button className="btn-hint-solution" onClick={onShowSolution}>
          📝 Show full solution
        </button>
      </div>
    </div>
  );
}
