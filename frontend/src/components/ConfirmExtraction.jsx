import MarkdownRenderer from "./MarkdownRenderer";

/**
 * ConfirmExtraction — shown when the image extraction agent returns
 * extracted text and the system needs user confirmation before solving.
 */
export default function ConfirmExtraction({ text, onConfirm, onCancel }) {
  return (
    <div className="message confirm-extraction">
      <div className="confirm-header">Extracted Question</div>
      <div className="confirm-body">
        <MarkdownRenderer content={text} />
      </div>
      <div className="confirm-prompt">
        Does this look correct?
      </div>
      <div className="confirm-actions">
        <button className="btn-confirm" onClick={() => onConfirm(text)}>
          ✓ Yes, solve this
        </button>
        <button className="btn-cancel" onClick={onCancel}>
          ✕ No, cancel
        </button>
      </div>
    </div>
  );
}
