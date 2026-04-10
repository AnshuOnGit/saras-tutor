import MarkdownRenderer from "./MarkdownRenderer";

function MetadataBadge({ label, value }) {
  return (
    <span className="meta-badge">
      <span className="label">{label}</span>
      <span className="value">{value}</span>
    </span>
  );
}

function MetadataMessage({ meta }) {
  const agent = meta.agent || "—";
  const model = meta.model || "unknown";
  const promptTok = meta.prompt_tokens ?? "—";
  const completionTok = meta.completion_tokens ?? "—";
  const totalTok = meta.total_tokens ?? "—";
  const passed = meta.passed;

  // Verifier metadata uses "score" + "pass" number
  if (agent === "verifier") {
    const score = meta.score != null ? (meta.score * 100).toFixed(0) : "?";
    const threshold = meta.threshold != null ? (meta.threshold * 100).toFixed(0) : "70";
    const passNum = meta.pass || 1;
    const issues = meta.issues || "";

    return (
      <div className={`message metadata${passed ? "" : " low"}`}>
        <MetadataBadge label="agent" value={`verifier (pass ${passNum})`} />
        <MetadataBadge label="score" value={`${score}%`} />
        <MetadataBadge label="threshold" value={`${threshold}%`} />
        <MetadataBadge label="status" value={passed ? "✓ accepted" : "✗ low quality"} />
        <MetadataBadge label="model" value={model} />
        <MetadataBadge label="tokens" value={`${promptTok} → ${completionTok} (${totalTok})`} />
        {issues && <MetadataBadge label="issues" value={issues} />}
      </div>
    );
  }

  // Confidence-based metadata (hint agent)
  const confidence = meta.confidence != null ? (meta.confidence * 100).toFixed(0) : "?";
  const threshold = meta.threshold != null ? (meta.threshold * 100).toFixed(0) : "90";

  return (
    <div className={`message metadata${passed ? "" : " low"}`}>
      <MetadataBadge label="agent" value={agent} />
      <MetadataBadge label="confidence" value={`${confidence}%`} />
      <MetadataBadge label="threshold" value={`${threshold}%`} />
      <MetadataBadge label="status" value={passed ? "✓ passed" : "✗ retry w/ image"} />
      <MetadataBadge label="model" value={model} />
      <MetadataBadge label="tokens" value={`${promptTok} → ${completionTok} (${totalTok})`} />
    </div>
  );
}

export default function MessageBubble({ message }) {
  const { role, text, imagePreview, meta } = message;

  // Metadata messages get their own renderer
  if (role === "metadata" && meta) {
    return <MetadataMessage meta={meta} />;
  }

  // Only render markdown for assistant messages — user/transition/status/error stay plain
  const useMarkdown = role === "assistant";

  return (
    <div className={`message ${role}`}>
      {imagePreview && (
        <img src={imagePreview} alt="uploaded" className="image-preview" />
      )}
      {text && (
        useMarkdown ? <MarkdownRenderer content={text} /> : <div>{text}</div>
      )}
    </div>
  );
}
