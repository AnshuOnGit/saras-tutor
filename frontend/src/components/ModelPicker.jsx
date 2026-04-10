/**
 * ModelPicker — shown when the verifier gives a low score even after retrying
 * with the original image. Offers alternative models to re-solve the question.
 */
export default function ModelPicker({ models, onPickModel, onDismiss }) {
  if (!models || models.length === 0) return null;

  return (
    <div className="message model-picker">
      <div className="model-picker-header">
        🔄 Try with a different model?
      </div>
      <div className="model-picker-buttons">
        {models.map((model) => (
          <button
            key={model}
            className="btn-model"
            onClick={() => onPickModel(model)}
          >
            {model}
          </button>
        ))}
        <button className="btn-model-dismiss" onClick={onDismiss}>
          ✗ Keep current answer
        </button>
      </div>
    </div>
  );
}
