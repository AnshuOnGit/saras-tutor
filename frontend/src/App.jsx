import { useState, useRef, useEffect, useCallback } from "react";
import MessageBubble from "./components/MessageBubble";
import HintActions from "./components/HintActions";
import ModelPicker from "./components/ModelPicker";
import InputBar from "./components/InputBar";

const DEFAULT_USER_ID = "test-user";
const DEFAULT_SESSION_ID = crypto.randomUUID();

export default function App() {
  const [messages, setMessages] = useState([]);
  const [streaming, setStreaming] = useState(false);
  const [userId, setUserId] = useState(DEFAULT_USER_ID);
  const [sessionId, setSessionId] = useState(DEFAULT_SESSION_ID);
  // Hint state: { hintLevel, question } when hint agent is waiting for student response
  const [pendingHint, setPendingHint] = useState(null);
  // Model picker state: { models: [...] } when verifier rejects solution
  const [pendingModelPicker, setPendingModelPicker] = useState(null);
  const bottomRef = useRef(null);

  // Auto-scroll on new messages
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const sendMessage = useCallback(
    async (text, imageFile, action = "new_question") => {
      // Add user message to UI (skip for button actions with no text)
      if (text || imageFile) {
        const userMsg = {
          id: crypto.randomUUID(),
          role: "user",
          text: text || (action === "more_help" ? "Give me another hint" : action === "show_solution" ? "Show full solution" : ""),
          imagePreview: imageFile ? URL.createObjectURL(imageFile) : null,
        };
        setMessages((prev) => [...prev, userMsg]);
      }
      setStreaming(true);

      // Build the request
      let body;
      let headers = {};

      if (imageFile) {
        const fd = new FormData();
        fd.append("user_id", userId);
        fd.append("session_id", sessionId);
        fd.append("action", action);
        fd.append("text", text || "");
        fd.append("image", imageFile);
        body = fd;
        // Don't set Content-Type — browser sets multipart boundary
      } else {
        headers["Content-Type"] = "application/json";
        body = JSON.stringify({
          user_id: userId,
          session_id: sessionId,
          action,
          message: { content_type: "text", text: text || "" },
        });
      }

      try {
        const res = await fetch("/chat", { method: "POST", headers, body });

        if (!res.ok) {
          const errText = await res.text();
          setMessages((prev) => [
            ...prev,
            { id: crypto.randomUUID(), role: "error", text: `Error ${res.status}: ${errText}` },
          ]);
          setStreaming(false);
          return;
        }

        // Read SSE stream
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        let assistantId = crypto.randomUUID();
        let assistantText = "";

        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });

          // Split by double-newline (SSE frame boundary)
          const frames = buffer.split("\n\n");
          buffer = frames.pop(); // keep incomplete frame

          for (const frame of frames) {
            const line = frame.trim();
            if (!line.startsWith("data: ")) continue;
            const payload = line.slice(6);
            if (payload === "[DONE]") continue;

            try {
              const ev = JSON.parse(payload);

              // new_turn = verifier rejected pass 1, pass 2 starting.
              // Reset so pass 2 streams into a fresh assistant bubble.
              if (ev.type === "new_turn") {
                assistantId = crypto.randomUUID();
                assistantText = "";
                continue;
              }

              handleSSEEvent(ev, assistantId, assistantText, (newText) => {
                assistantText = newText;
                setMessages((prev) => {
                  const existing = prev.find((m) => m.id === assistantId);
                  if (existing) {
                    return prev.map((m) => (m.id === assistantId ? { ...m, text: assistantText } : m));
                  }
                  return [
                    ...prev,
                    { id: assistantId, role: "assistant", text: assistantText },
                  ];
                });
              });
            } catch {
              // ignore parse errors
            }
          }
        }
      } catch (err) {
        setMessages((prev) => [
          ...prev,
          { id: crypto.randomUUID(), role: "error", text: `Network error: ${err.message}` },
        ]);
      }

      setStreaming(false);
    },
    [userId, sessionId]
  );

  function handleSSEEvent(ev, assistantId, currentText, updateAssistant) {
    switch (ev.type) {
      case "artifact":
        if (ev.message?.parts) {
          for (const p of ev.message.parts) {
            if (p.type === "text") {
              updateAssistant(currentText + p.text);
            }
          }
        }
        break;

      case "transition":
        setMessages((prev) => [
          ...prev,
          {
            id: crypto.randomUUID(),
            role: "transition",
            text: `${ev.from_agent || "?"} → ${ev.to_agent || "?"}: ${ev.reason || ""}`,
          },
        ]);
        break;

      case "status":
        // input-needed can mean two things:
        // 1. Image extraction confirmation (text is the extracted content)
        // 2. Hint follow-up (text is JSON with hint_level and question)
        if (ev.state === "input-needed" && ev.message?.parts) {
          const rawText = ev.message.parts
            .filter((p) => p.type === "text")
            .map((p) => p.text)
            .join("\n");

          // Try to detect hint metadata (JSON with hint_level)
          try {
            const meta = JSON.parse(rawText);
            if (meta.model_picker && (meta.models || meta.proceed_action)) {
              // Solver / hint / vision / evaluator offered alternatives
              setPendingModelPicker({
                models: meta.models || [],
                category: meta.category || "",
                current: meta.current || "",
                optional: meta.optional === true,
                reason: meta.reason || "",
                proceedAction: meta.proceed_action || "",
                interactionId: meta.interaction_id || "",
              });
            } else if (meta.attempt_evaluated) {
              // Attempt was evaluated but not fully correct — re-show hint actions
              setPendingHint({
                hintLevel: meta.hint_level,
                attemptScore: meta.score,
              });
            } else if (meta.hint_level !== undefined) {
              setPendingHint({
                hintLevel: meta.hint_level,
                question: meta.question || "",
              });
            }
          } catch {
            // Not JSON — ignore
          }
          break;
        }
        if (ev.state && ev.state !== "completed") {
          setMessages((prev) => [
            ...prev,
            {
              id: crypto.randomUUID(),
              role: "status",
              text: `Status: ${ev.state}`,
            },
          ]);
        }
        break;

      case "metadata":
        if (ev.meta) {
          setMessages((prev) => [
            ...prev,
            {
              id: crypto.randomUUID(),
              role: "metadata",
              meta: ev.meta,
            },
          ]);
        }
        break;

      case "error":
        setMessages((prev) => [
          ...prev,
          { id: crypto.randomUUID(), role: "error", text: ev.error || "Unknown error" },
        ]);
        break;

      default:
        break;
    }
  }

  // Hint flow handlers
  function handleMoreHelp() {
    setPendingHint(null);
    sendMessage("", null, "more_help");
  }

  function handleShowSolution() {
    setPendingHint(null);
    sendMessage("", null, "show_solution");
  }

  function handleDismissHint() {
    setPendingHint(null);
    sendMessage("", null, "close");
  }

  // Model picker handlers
  function handlePickModel(model, category) {
    setPendingModelPicker(null);
    sendRetryModel(model, category);
  }

  function handleDismissModelPicker() {
    setPendingModelPicker(null);
  }

  // Primary "Proceed" button on a gated picker (e.g. extract_proceed).
  // Dispatches the named action with no model override, continuing the flow.
  function handleProceedModelPicker(action) {
    setPendingModelPicker(null);
    sendMessage("", null, action);
  }

  // Send a retry_model request with the selected model
  const sendRetryModel = useCallback(
    async (model, category) => {
      setMessages((prev) => [
        ...prev,
        {
          id: crypto.randomUUID(),
          role: "user",
          text: `Retry with model: ${model}${category ? ` (${category})` : ""}`,
        },
      ]);
      setStreaming(true);

      const headers = { "Content-Type": "application/json" };
      const body = JSON.stringify({
        user_id: userId,
        session_id: sessionId,
        action: "retry_model",
        model: model,
        category: category || "",
        message: { content_type: "text", text: "" },
      });

      try {
        const res = await fetch("/chat", { method: "POST", headers, body });
        if (!res.ok) {
          const errText = await res.text();
          setMessages((prev) => [
            ...prev,
            { id: crypto.randomUUID(), role: "error", text: `Error ${res.status}: ${errText}` },
          ]);
          setStreaming(false);
          return;
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        let assistantId = crypto.randomUUID();
        let assistantText = "";

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

              if (ev.type === "new_turn") {
                assistantId = crypto.randomUUID();
                assistantText = "";
                continue;
              }

              handleSSEEvent(ev, assistantId, assistantText, (newText) => {
                assistantText = newText;
                setMessages((prev) => {
                  const existing = prev.find((m) => m.id === assistantId);
                  if (existing) {
                    return prev.map((m) => (m.id === assistantId ? { ...m, text: assistantText } : m));
                  }
                  return [
                    ...prev,
                    { id: assistantId, role: "assistant", text: assistantText },
                  ];
                });
              });
            } catch {
              // ignore
            }
          }
        }
      } catch (err) {
        setMessages((prev) => [
          ...prev,
          { id: crypto.randomUUID(), role: "error", text: `Network error: ${err.message}` },
        ]);
      }

      setStreaming(false);
    },
    [userId, sessionId]
  );

  return (
    <div className="app">
      <header className="header">
        <h1>Saras Tutor</h1>
        <div className="header-controls">
          <input
            type="text"
            placeholder="User ID"
            value={userId}
            onChange={(e) => setUserId(e.target.value)}
          />
          <input
            type="text"
            placeholder="Session ID"
            value={sessionId}
            onChange={(e) => setSessionId(e.target.value)}
          />
        </div>
      </header>

      <div className="messages">
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} />
        ))}
        {pendingHint && !streaming && (
          <HintActions
            onShowSolution={handleShowSolution}
            onDismiss={handleDismissHint}
          />
        )}
        {pendingModelPicker && !streaming && (
          <ModelPicker
            models={pendingModelPicker.models}
            category={pendingModelPicker.category}
            current={pendingModelPicker.current}
            optional={pendingModelPicker.optional}
            reason={pendingModelPicker.reason}
            proceedAction={pendingModelPicker.proceedAction}
            onPickModel={handlePickModel}
            onProceed={handleProceedModelPicker}
            onDismiss={handleDismissModelPicker}
          />
        )}
        {streaming && (
          <div className="typing">
            <span>●</span> <span>●</span> <span>●</span>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      <InputBar
        onSend={(text, imageFile) => {
          if (pendingHint) {
            // Student is submitting their attempt after receiving a hint
            setPendingHint(null);
            sendMessage(text, imageFile, "submit_attempt");
          } else {
            sendMessage(text, imageFile);
          }
        }}
        // Disable typing only while streaming or while a GATED picker (e.g.
        // the extraction confirmation) is pending. Optional pickers — the
        // "Not satisfied? Try another model" bar — leave input enabled so the
        // student can still type an attempt.
        disabled={
          streaming ||
          (!!pendingModelPicker &&
            (pendingModelPicker.proceedAction ||
              pendingModelPicker.optional === false))
        }
        placeholder={pendingHint ? "Type your attempt or attach a photo of your work…" : undefined}
      />
    </div>
  );
}
