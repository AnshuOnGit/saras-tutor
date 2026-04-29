import { useState, useEffect, useRef, useCallback } from "react";
import Markdown from "./components/Markdown";
import LandingPage from "./components/LandingPage";
import ImageCropper from "./components/ImageCropper";
import { useAuth } from "./context/AuthContext";
import API_BASE from "./api";
import logoSvg from "./assets/logo.svg";

const SESSION_ID = crypto.randomUUID();

export default function App() {
  const { user, loading, logout } = useAuth();

  if (loading) {
    return (
      <div className="loading-screen">
        <img src={logoSvg} alt="Saras" className="loading-logo" />
        <div className="spinner" />
      </div>
    );
  }

  if (!user) {
    return <LandingPage />;
  }

  return <Studio user={user} logout={logout} />;
}

function Studio({ user, logout }) {
  const USER_ID = user.id;
  // ── Models ─────────────────────────────────────────────────────
  const [categories, setCategories] = useState([]);
  const [activeTab, setActiveTab] = useState("OCR");
  const [selectedOCR, setSelectedOCR] = useState("");
  const [selectedSolver, setSelectedSolver] = useState("");

  // ── Upload ─────────────────────────────────────────────────────
  const [imageFile, setImageFile] = useState(null);
  const [imagePreview, setImagePreview] = useState(null);
  const [extracting, setExtracting] = useState(false);
  const fileInputRef = useRef(null);
  const [dragOverUpload, setDragOverUpload] = useState(false);

  // ── Extractions ────────────────────────────────────────────────
  const [extractions, setExtractions] = useState([]);
  const [expandedExtraction, setExpandedExtraction] = useState(null);

  // ── Workspace (right panel drop zone) ──────────────────────────
  // Each slot: { extraction, role: "question"|"attempt" }
  const [workspace, setWorkspace] = useState([]);
  const [dragOverWorkspace, setDragOverWorkspace] = useState(false);
  const [draggedExtraction, setDraggedExtraction] = useState(null);
  const [editingSlotId, setEditingSlotId] = useState(null);

  // ── Conversation ───────────────────────────────────────────────
  // messages: [{ id, role: "user"|"assistant", content }]
  const [messages, setMessages] = useState([]);
  const [conversationId, setConversationId] = useState(crypto.randomUUID());
  const [streaming, setStreaming] = useState(false);
  const [followUp, setFollowUp] = useState("");
  const chatEndRef = useRef(null);
  const solverBodyRef = useRef(null);

  // ── Mobile panel toggle ────────────────────────────────────────
  const [mobilePanel, setMobilePanel] = useState("extract"); // "extract" | "workspace"

  // ── Load models ────────────────────────────────────────────────
  useEffect(() => {
    fetch(`${API_BASE}/api/models`)
      .then((r) => r.json())
      .then((data) => {
        setCategories(data.categories || []);
        const ocr = data.categories?.find((c) => c.category === "OCR");
        const solver = data.categories?.find((c) => c.category === "Solver");
        if (ocr?.default) setSelectedOCR(ocr.default);
        if (solver?.default) setSelectedSolver(solver.default);
      })
      .catch(console.error);
  }, []);

  // ── Load extractions ──────────────────────────────────────────
  useEffect(() => {
    fetch(`${API_BASE}/api/extractions?session_id=${SESSION_ID}&user_id=${USER_ID}`)
      .then((r) => r.json())
      .then((data) => setExtractions(data.extractions || []))
      .catch(console.error);
  }, []);

  // ── Auto-scroll chat (only if user is near the bottom) ─────
  const userScrolledUp = useRef(false);

  useEffect(() => {
    const el = solverBodyRef.current;
    if (!el) return;
    const handleScroll = () => {
      const threshold = 150;
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
      userScrolledUp.current = !atBottom;
    };
    el.addEventListener("scroll", handleScroll, { passive: true });
    return () => el.removeEventListener("scroll", handleScroll);
  }, []);

  useEffect(() => {
    if (!userScrolledUp.current) {
      const el = solverBodyRef.current;
      if (el) {
        el.scrollTop = el.scrollHeight;
      }
    }
  }, [messages]);

  // ── Cropper ────────────────────────────────────────────────────
  const [cropperSrc, setCropperSrc] = useState(null);

  // ── Upload handlers ───────────────────────────────────────────
  const handleFile = (file) => {
    if (!file || !file.type.startsWith("image/")) return;
    // Show cropper first
    setCropperSrc(URL.createObjectURL(file));
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const handleCropped = (croppedFile, previewUrl) => {
    setImageFile(croppedFile);
    setImagePreview(previewUrl);
    setCropperSrc(null);
  };

  const cancelCrop = () => {
    setCropperSrc(null);
  };

  const clearImage = () => {
    setImageFile(null);
    setImagePreview(null);
    setCropperSrc(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const doExtract = async () => {
    if (!imageFile || extracting) return;
    setExtracting(true);
    try {
      const fd = new FormData();
      fd.append("session_id", SESSION_ID);
      fd.append("user_id", USER_ID);
      fd.append("model", selectedOCR);
      fd.append("image", imageFile);
      const res = await fetch(`${API_BASE}/api/extract`, { method: "POST", body: fd });
      if (!res.ok) {
        const err = await res.json();
        alert("Extraction failed: " + (err.error || res.statusText));
        return;
      }
      const extraction = await res.json();
      setExtractions((prev) => [extraction, ...prev]);
      clearImage();
    } catch (e) {
      alert("Extraction error: " + e.message);
    } finally {
      setExtracting(false);
    }
  };

  // ── Drag from extraction list ─────────────────────────────────
  const handleDragStart = (e, extraction) => {
    setDraggedExtraction(extraction);
    e.dataTransfer.effectAllowed = "copy";
    e.dataTransfer.setData("text/plain", extraction.id);
  };

  // ── Drop into workspace ───────────────────────────────────────
  const handleWorkspaceDragOver = (e) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDragOverWorkspace(true);
  };
  const handleWorkspaceDragLeave = () => setDragOverWorkspace(false);
  const handleWorkspaceDrop = (e) => {
    e.preventDefault();
    setDragOverWorkspace(false);
    if (draggedExtraction) {
      addToWorkspace(draggedExtraction);
      setDraggedExtraction(null);
    }
  };

  const addToWorkspace = (extraction) => {
    if (workspace.some((s) => s.extraction.id === extraction.id)) return;
    const hasQ = workspace.some((s) => s.role === "question");
    setWorkspace((prev) => [
      ...prev,
      { extraction, role: hasQ ? "attempt" : "question", editedText: extraction.extracted_text },
    ]);
  };

  const updateSlotText = (extractionId, newText) => {
    setWorkspace((prev) =>
      prev.map((s) =>
        s.extraction.id === extractionId ? { ...s, editedText: newText } : s
      )
    );
  };

  const removeFromWorkspace = (extractionId) => {
    setWorkspace((prev) => prev.filter((s) => s.extraction.id !== extractionId));
  };

  const toggleRole = (extractionId) => {
    setWorkspace((prev) =>
      prev.map((s) =>
        s.extraction.id === extractionId
          ? { ...s, role: s.role === "question" ? "attempt" : "question" }
          : s
      )
    );
  };

  const clearConversation = () => {
    setMessages([]);
    setWorkspace([]);
    setConversationId(crypto.randomUUID());
  };

  // ── Stream SSE ────────────────────────────────────────────────
  const streamingRef = useRef(false);
  const streamChat = useCallback(
    async (intent, extraMessage) => {
      if (streamingRef.current) return;
      streamingRef.current = true;
      setStreaming(true);

      const intentLabels = {
        solve: "🧠 Solve",
        hint: "💡 Hint",
        evaluate: "📝 Evaluate",
        followup: "💬",
      };

      let userContent = extraMessage || "";
      if (intent !== "followup") {
        const slotDescs = workspace.map(
          (s) =>
            `[${s.role === "question" ? "📋 Question" : "✍️ Attempt"}] ${s.extraction.extracted_text.slice(0, 80)}…`
        );
        userContent = `${intentLabels[intent] || intent}${slotDescs.length ? "\n\n" + slotDescs.join("\n") : ""}`;
      }

      const userMsg = { id: crypto.randomUUID(), role: "user", content: userContent };
      const assistantId = crypto.randomUUID();
      setMessages((prev) => [
        ...prev,
        userMsg,
        { id: assistantId, role: "assistant", content: "" },
      ]);

      const history = messages
        .filter((m) => m.role === "user" || m.role === "assistant")
        .map((m) => ({ role: m.role, content: m.content }));

      const body = {
        session_id: SESSION_ID,
        user_id: USER_ID,
        conversation_id: conversationId,
        model: selectedSolver,
        intent,
        slots: workspace.map((s) => ({
          extraction_id: s.extraction.id,
          role: s.role,
          text: s.editedText || s.extraction.extracted_text,
        })),
        message: extraMessage || "",
        history,
      };

      try {
        const res = await fetch(`${API_BASE}/api/chat`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });

        if (!res.ok) {
          const errData = await res.json().catch(() => ({ error: res.statusText }));
          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantId ? { ...m, content: `**Error:** ${errData.error}` } : m
            )
          );
          setStreaming(false);
          return;
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        let fullText = "";

        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const frames = buffer.split("\n\n");
          buffer = frames.pop();
          for (const frame of frames) {
            const line = frame.trim();
            if (!line.startsWith("data: ")) continue;
            const payload = line.slice(6);
            if (payload === "[DONE]") continue;
            try {
              const ev = JSON.parse(payload);
              if (ev.type === "token") {
                fullText += ev.text;
                setMessages((prev) =>
                  prev.map((m) => (m.id === assistantId ? { ...m, content: fullText } : m))
                );
              }
            } catch {}
          }
        }
      } catch (e) {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, content: m.content + "\n\n**Error:** " + e.message } : m
          )
        );
      } finally {
        streamingRef.current = false;
        setStreaming(false);
      }
    },
    [selectedSolver, workspace, messages, conversationId]
  );

  const handleFollowUp = (e) => {
    e.preventDefault();
    if (!followUp.trim() || streaming) return;
    const msg = followUp.trim();
    setFollowUp("");
    streamChat("followup", msg);
  };

  // ── Helpers ───────────────────────────────────────────────────
  const currentCategory = categories.find((c) => c.category === activeTab);
  const selectedModelId = activeTab === "OCR" ? selectedOCR : selectedSolver;
  const setSelectedModel = activeTab === "OCR" ? setSelectedOCR : setSelectedSolver;

  const getModelDisplayName = (id) => {
    for (const cat of categories) {
      for (const prov of cat.providers || []) {
        for (const m of prov.models || []) {
          if (m.id === id) return m.display_name;
        }
      }
    }
    return id;
  };

  const hasQuestion = workspace.some((s) => s.role === "question");
  const hasAttempt = workspace.some((s) => s.role === "attempt");
  const canEvaluate = hasQuestion && hasAttempt;

  // ─────────────────────────────────────────────────────────────
  return (
    <div className="studio-layout">
      {/* ─── MOBILE NAV ─────────────────────────────────────────── */}
      <div className="mobile-panel-nav">
        <button className={`mobile-panel-btn ${mobilePanel === "extract" ? "active" : ""}`} onClick={() => setMobilePanel("extract")}>
          📷 Extract
        </button>
        <button className={`mobile-panel-btn ${mobilePanel === "workspace" ? "active" : ""}`} onClick={() => setMobilePanel("workspace")}>
          🧠 Workspace {workspace.length > 0 && <span className="mobile-badge">{workspace.length}</span>}
        </button>
      </div>

      {/* ─── LEFT PANEL ─────────────────────────────────────────── */}
      <div className={`left-panel ${mobilePanel === "extract" ? "mobile-visible" : "mobile-hidden"}`}>
        <div className="panel-header">
          <img src={logoSvg} alt="Saras" style={{ width: 24, height: 24 }} />
          <h2>Saras Studio</h2>
          <div className="user-menu">
            {user.picture ? (
              <img src={user.picture} alt={user.name} className="user-avatar" />
            ) : (
              <span className="user-avatar-placeholder">{user.name?.[0] || "?"}</span>
            )}
            <button className="btn-sm btn-logout" onClick={logout} title="Sign out">↪ Sign out</button>
          </div>
        </div>

        {/* Model selector */}
        <div className="model-section">
          <div className="model-section-title">Model Selection</div>
          <div className="model-tabs">
            <button className={`model-tab ${activeTab === "OCR" ? "active-ocr" : ""}`} onClick={() => setActiveTab("OCR")}>
              🔍 OCR <span className="count">{categories.find((c) => c.category === "OCR")?.total_models || 0}</span>
            </button>
            <button className={`model-tab ${activeTab === "Solver" ? "active-solver" : ""}`} onClick={() => setActiveTab("Solver")}>
              🧠 Solver <span className="count">{categories.find((c) => c.category === "Solver")?.total_models || 0}</span>
            </button>
          </div>
          <div className="models-scroll">
            {currentCategory?.providers?.map((prov) => (
              <div key={prov.provider} className="provider-group">
                <div className="provider-name">{prov.provider}</div>
                <div className="model-list">
                  {prov.models.map((m) => (
                    <div key={m.id} className={`model-item ${selectedModelId === m.id ? "selected" : ""} ${activeTab === "OCR" ? "ocr-item" : ""}`} onClick={() => setSelectedModel(m.id)}>
                      <div className="radio" />
                      <div className="model-info">
                        <div className="model-name">{m.display_name}</div>
                        {m.notes && <div className="model-notes">{m.notes}</div>}
                      </div>
                      {m.priority === 1 && <span className="model-default-badge">DEFAULT</span>}
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Upload area */}
        <div className="upload-section">
          <div className={`upload-drop-zone ${dragOverUpload ? "drag-over" : ""}`} onClick={() => fileInputRef.current?.click()} onDragOver={(e) => { e.preventDefault(); setDragOverUpload(true); }} onDragLeave={() => setDragOverUpload(false)} onDrop={(e) => { e.preventDefault(); setDragOverUpload(false); handleFile(e.dataTransfer.files[0]); }}>
            <div className="icon">📷</div>
            <div className="label">Drop an image or click to upload</div>
            <div className="hint">Supports JPG, PNG — will be resized to max 1568px</div>
          </div>
          <input ref={fileInputRef} type="file" accept="image/*" capture="environment" hidden onChange={(e) => handleFile(e.target.files[0])} />

          {imagePreview && (
            <div className="upload-preview">
              <img src={imagePreview} alt="preview" />
              <div className="info">
                <div className="filename">{imageFile?.name}</div>
                <div className="meta">{(imageFile?.size / 1024).toFixed(0)} KB • {getModelDisplayName(selectedOCR)}</div>
              </div>
              <button className="btn-clear" onClick={clearImage}>×</button>
            </div>
          )}

          {extracting && (
            <div className="extracting-overlay">
              <div className="spinner" />
              <div className="label">Extracting with {getModelDisplayName(selectedOCR)}…</div>
            </div>
          )}

          <button className="btn btn-extract" disabled={!imageFile || extracting} onClick={doExtract}>
            {extracting ? (<><div className="spinner" /> Extracting…</>) : (<>🔍 Extract Text</>)}
          </button>
        </div>

        {/* Extractions list */}
        <div className="extractions-section">
          <div className="extractions-title">
            Extractions <span className="count">{extractions.length}</span>
          </div>

          {extractions.length === 0 ? (
            <div className="empty-state">
              <div className="icon">📋</div>
              <div className="title">No extractions yet</div>
              <div className="desc">Upload an image above and extract text using any OCR model</div>
            </div>
          ) : (
            extractions.map((ext) => {
              const inWs = workspace.some((s) => s.extraction.id === ext.id);
              return (
                <div key={ext.id} className={`extraction-card ${inWs ? "in-workspace" : ""}`} draggable onDragStart={(e) => handleDragStart(e, ext)}>
                  <div className="card-header">
                    <span className="card-model">{getModelDisplayName(ext.model_id)}</span>
                    <span className="card-time">{new Date(ext.created_at).toLocaleTimeString()}</span>
                    <button className="btn-expand" onClick={(e) => { e.stopPropagation(); setExpandedExtraction(ext); }} title="Expand">⛶</button>
                  </div>
                  <div className="card-text"><Markdown>{ext.extracted_text}</Markdown></div>
                  <div className="card-actions">
                    {!inWs ? (
                      <button className="btn-sm btn-add-workspace" onClick={() => addToWorkspace(ext)}>＋ Add to workspace</button>
                    ) : (
                      <span className="btn-sm in-workspace-badge">✓ In workspace</span>
                    )}
                    <button className="btn-sm" title="Drag to workspace">⇥ Drag</button>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>

      {/* ─── RIGHT PANEL ────────────────────────────────────────── */}
      <div className={`right-panel ${mobilePanel === "workspace" ? "mobile-visible" : "mobile-hidden"}`}>
        <div className="solver-header">
          <span style={{ fontSize: 20 }}>🧠</span>
          <h2>Workspace</h2>
          <div className="solver-model-indicator">
            <span className="dot" />
            {getModelDisplayName(selectedSolver)}
          </div>
          {(workspace.length > 0 || messages.length > 0) && (
            <button className="btn-sm btn-clear-conv" onClick={clearConversation}>✕ Clear</button>
          )}
        </div>

        <div className="solver-body" ref={solverBodyRef}>
          {/* Drop zone + slots */}
          <div
            className={`workspace-drop-area ${dragOverWorkspace ? "drag-over" : ""} ${workspace.length === 0 && messages.length === 0 ? "empty" : ""}`}
            onDragOver={handleWorkspaceDragOver}
            onDragLeave={handleWorkspaceDragLeave}
            onDrop={handleWorkspaceDrop}
          >
            {workspace.length === 0 && messages.length === 0 ? (
              <div className="workspace-empty-hint">
                <div className="icon">🎯</div>
                <div className="title">Drag extractions here</div>
                <div className="desc">
                  Drop one or more extraction cards from the left panel.<br />
                  Label them as <strong>Question</strong> or <strong>Attempt</strong>, then choose an action below.
                </div>
              </div>
            ) : workspace.length > 0 ? (
              <div className="workspace-slots">
                <div className="workspace-slots-title">📌 Workspace Slots <span className="count">{workspace.length}</span></div>
                {workspace.map((slot) => (
                  <div key={slot.extraction.id} className={`workspace-slot role-${slot.role}`}>
                    <div className="slot-header">
                      <button className={`role-toggle role-${slot.role}`} onClick={() => toggleRole(slot.extraction.id)} title="Click to toggle role">
                        {slot.role === "question" ? "📋 Question" : "✍️ Attempt"}
                      </button>
                      <span className="slot-model">{getModelDisplayName(slot.extraction.model_id)}</span>
                      <button className="btn-clear slot-remove" onClick={() => removeFromWorkspace(slot.extraction.id)}>×</button>
                    </div>
                    <div className="slot-text-wrap">
                      {editingSlotId === slot.extraction.id ? (
                        <>
                          <textarea
                            className="slot-text-editor"
                            value={slot.editedText ?? slot.extraction.extracted_text}
                            onChange={(e) => updateSlotText(slot.extraction.id, e.target.value)}
                            spellCheck={false}
                            autoFocus
                          />
                          <button className="btn-slot-toggle" onClick={() => setEditingSlotId(null)} title="Done editing">✅ Done</button>
                        </>
                      ) : (
                        <>
                          <div className="slot-text-rendered"><Markdown>{slot.editedText ?? slot.extraction.extracted_text}</Markdown></div>
                          <button className="btn-slot-toggle" onClick={() => setEditingSlotId(slot.extraction.id)} title="Edit extracted text">✏️ Edit text</button>
                        </>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            ) : null}
          </div>

          {/* Intent buttons */}
          {workspace.length > 0 && (
            <div className="intent-section">
              <div className="intent-bar">
                <button className="btn btn-intent btn-intent-solve" onClick={() => streamChat("solve")} disabled={streaming || !hasQuestion} title={hasQuestion ? "Full step-by-step solution" : "Add a Question first"}>
                  🧠 Solve
                </button>
                <button className="btn btn-intent btn-intent-hint" onClick={() => streamChat("hint")} disabled={streaming || !hasQuestion} title={hasQuestion ? "Get a hint without the answer" : "Add a Question first"}>
                  💡 Hint
                </button>
                <button className="btn btn-intent btn-intent-evaluate" onClick={() => streamChat("evaluate")} disabled={streaming || !canEvaluate} title={canEvaluate ? "Evaluate attempt against question" : "Need both Question and Attempt"}>
                  📝 Evaluate
                </button>
              </div>
            </div>
          )}

          {/* Chat messages */}
          {messages.length > 0 && (
            <div className="chat-messages">
              {messages.map((msg) => (
                <div key={msg.id} className={`chat-msg chat-msg-${msg.role}`}>
                  <div className="chat-msg-avatar">{msg.role === "user" ? "👤" : "🤖"}</div>
                  <div className={`chat-msg-body ${msg.role === "assistant" && msg.content === "" ? "streaming-cursor" : ""}`}>
                    <Markdown>{msg.content || "Thinking…"}</Markdown>
                  </div>
                </div>
              ))}
              <div ref={chatEndRef} />
            </div>
          )}

          {/* Follow-up input */}
          {messages.length > 0 && (
            <form className="follow-up-bar" onSubmit={handleFollowUp}>
              <textarea
                rows={3}
                className="follow-up-input"
                placeholder="Ask a follow-up question…"
                value={followUp}
                onChange={(e) => setFollowUp(e.target.value)}
                disabled={streaming}
                onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleFollowUp(e); } }}
              />
              <button className="btn btn-follow-up" type="submit" disabled={streaming || !followUp.trim()}>
                {streaming ? <div className="spinner solver-spinner" /> : "Send"}
              </button>
            </form>
          )}
        </div>
      </div>

      {/* ─── IMAGE CROPPER MODAL ────────────────────────────── */}
      {cropperSrc && (
        <ImageCropper
          imageSrc={cropperSrc}
          onCropped={handleCropped}
          onCancel={cancelCrop}
        />
      )}

      {/* ─── EXPANDED EXTRACTION MODAL ──────────────────────────── */}
      {expandedExtraction && (
        <div className="modal-overlay" onClick={() => setExpandedExtraction(null)}>
          <div className="modal-content modal-content--wide" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="card-model">{getModelDisplayName(expandedExtraction.model_id)}</span>
              <span className="card-time">{new Date(expandedExtraction.created_at).toLocaleString()}</span>
              <div style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                <button className="btn-sm btn-add-workspace" onClick={() => { addToWorkspace(expandedExtraction); setExpandedExtraction(null); }}>＋ Add to workspace</button>
                <button className="btn-clear" onClick={() => setExpandedExtraction(null)}>✕</button>
              </div>
            </div>
            <div className="modal-split">
              <div className="modal-image-pane">
                <img src={expandedExtraction.image_url} alt="Extracted" className="modal-image" />
              </div>
              <div className="modal-body"><Markdown>{expandedExtraction.extracted_text}</Markdown></div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
