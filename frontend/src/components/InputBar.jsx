import { useState, useRef } from "react";

export default function InputBar({ onSend, disabled, placeholder }) {
  const [text, setText] = useState("");
  const [imageFile, setImageFile] = useState(null);
  const fileRef = useRef(null);
  const textRef = useRef(null);

  function handleSubmit(e) {
    e?.preventDefault();
    const trimmed = text.trim();
    if (!trimmed && !imageFile) return;
    onSend(trimmed, imageFile);
    setText("");
    setImageFile(null);
    if (fileRef.current) fileRef.current.value = "";
    // Refocus textarea
    setTimeout(() => textRef.current?.focus(), 50);
  }

  function handleKeyDown(e) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  }

  function handleFileChange(e) {
    const f = e.target.files?.[0];
    if (f) setImageFile(f);
  }

  function removeImage() {
    setImageFile(null);
    if (fileRef.current) fileRef.current.value = "";
  }

  return (
    <form className="input-bar" onSubmit={handleSubmit}>
      {/* Hidden file input */}
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        style={{ display: "none" }}
        onChange={handleFileChange}
      />

      {/* Attach image button */}
      <button
        type="button"
        className="btn-icon"
        title="Attach image"
        disabled={disabled}
        onClick={() => fileRef.current?.click()}
      >
        {/* Paperclip SVG */}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
        </svg>
      </button>

      {/* Image badge */}
      {imageFile && (
        <div className="image-badge">
          📷 {imageFile.name.length > 15 ? imageFile.name.slice(0, 15) + "…" : imageFile.name}
          <button type="button" onClick={removeImage} title="Remove image">✕</button>
        </div>
      )}

      {/* Text input */}
      <textarea
        ref={textRef}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder || "Type a message… (Shift+Enter for newline)"}
        rows={1}
        disabled={disabled}
      />

      {/* Send button */}
      <button
        type="submit"
        className="btn-icon btn-send"
        disabled={disabled || (!text.trim() && !imageFile)}
        title="Send"
      >
        {/* Arrow-up SVG */}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="12" y1="19" x2="12" y2="5" />
          <polyline points="5 12 12 5 19 12" />
        </svg>
      </button>
    </form>
  );
}
