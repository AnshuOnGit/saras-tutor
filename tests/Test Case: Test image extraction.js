Test Case: Test image extraction
http://localhost:5173/chat

data: {"type":"status","state":"working"}

data: {"type":"transition","from_agent":"router","to_agent":"image_extraction","task_id":"bc85bedb-397a-40a5-8eea-1eacb8340bd4","reason":"extracting text from image | model: meta/llama-3.2-90b-vision-instruct"}

data: {"type":"artifact","message":{"role":"agent","parts":[{"type":"text","text":"🔍 **Analysing image…** _(model: meta/llama-3.2-90b-vision-instruct)_\nThis may take 15-30 seconds for complex diagrams.\n\n"}]}}

data: {"type":"transition","from_agent":"image_extraction","to_agent":"router","task_id":"bc85bedb-397a-40a5-8eea-1eacb8340bd4","reason":"extraction complete (9.6s) | model: meta/llama-3.2-90b-vision-instruct"}

data: {"type":"artifact","message":{"role":"agent","parts":[{"type":"text","text":"📄 **Extracted from image** _(model: meta/llama-3.2-90b-vision-instruct)_:\n\nQ.11 Let $\\alpha$ and $\\beta$ be the real numbers such that\n\n$$\\lim_{x\\to0}\\frac{1}{x^3}\\left(\\alpha\\int_{0}^{x}\\frac{1}{1-t^2}dt+\\beta x\\cos x\\right)=2.$$\n\nThen the value of $\\alpha+\\beta$ is _______________________.\n\n---\n"}]}}

data: {"type":"status","state":"input-needed","message":{"role":"agent","parts":[{"type":"text","text":"{\"category\":\"Vision\",\"current\":\"meta/llama-3.2-90b-vision-instruct\",\"interaction_id\":\"7a9f84b4-8862-4e00-afe9-fbb5947e7282\",\"model_picker\":true,\"models\":[{\"id\":\"meta/llama-4-maverick-17b-128e-instruct\",\"display_name\":\"Llama 4 Maverick 17B\",\"notes\":\"Newer multimodal; faster, good for handwritten scans.\",\"priority\":2},{\"id\":\"meta/llama-3.2-11b-vision-instruct\",\"display_name\":\"Llama 3.2 11B Vision\",\"notes\":\"Lightweight vision model — use when 90B is throttled.\",\"priority\":3},{\"id\":\"nvidia/llama-3.1-nemotron-nano-vl-8b-v1\",\"display_name\":\"Nemotron Nano VL 8B\",\"notes\":\"Tiny VLM for low-latency simple images.\",\"priority\":4},{\"id\":\"microsoft/phi-3.5-vision-instruct\",\"display_name\":\"Phi-3.5 Vision\",\"notes\":\"Compact Microsoft VLM.\",\"priority\":5}],\"optional\":false,\"proceed_action\":\"extract_proceed\",\"reason\":\"Does the extracted text look correct? If not, try a different vision model.\"}"}]}}

data: [DONE]


