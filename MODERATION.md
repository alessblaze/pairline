# Auto-Moderation System

The auto-moderation system uses LLM-based safety classifiers to automatically evaluate user reports. It runs as a background worker in the Go admin service, polling for pending reports and sending chat evidence to a configurable model for safety assessment.

## Provider compatibility

The system uses the **OpenAI Chat Completions API schema** (`/v1/chat/completions`). Any provider that exposes this interface will work — it is **not locked to NVIDIA NIM**. Confirmed compatible providers include:

| Provider | Base URL | Example model |
|----------|----------|---------------|
| NVIDIA NIM (cloud) | `https://integrate.api.nvidia.com/v1` | `nvidia/llama-3.1-nemoguard-8b-content-safety` |
| NVIDIA NIM (self-hosted) | `http://nim.local:8000/v1` | Same as cloud |
| DeepSeek | `https://api.deepseek.com/v1` | `deepseek-v3.2` |
| Mistral | `https://api.mistral.ai/v1` | `mistral-large-latest` |
| Moonshot (Kimi) | `https://api.moonshot.cn/v1` | `kimi-k2-thinking` |
| StepFun | `https://api.stepfun.com/v1` | `stepfun-ai/step-3.5-flash` |
| Groq (for Gemma) | `https://api.groq.com/openai/v1` | `gemma2-9b-it` |
| Qwen | `https://api.qwen.ai/v1` | `qwen3.5-122b-a10b` |
| Any OpenAI-compatible | Your endpoint | Your model ID |

Set `AUTO_MODERATION_NIM_BASE_URL` to point at any compatible endpoint, and `AUTO_MODERATION_MODEL` to the model identifier the provider expects. Use `AUTO_MODERATION_MODEL_TYPE` to select the right prompt/parser adapter (see below).

## Architecture overview

```
┌─────────────────────────────────────────────────────┐
│                     Worker                          │
│  processSingleReport()                              │
│    ├─ extractPeerEvidence()     (reported user)     │
│    ├─ extractReporterEvidence() (reporter)          │
│    ├─ models.Resolve(modelType) → Adapter           │
│    │                                                │
│    ├─ DualAssessmentAdapter? ──yes──┐               │
│    │   callModelAPIMulti()          │               │
│    │   ParseDualAssessment()        │               │
│    │   (single API call, both)      │               │
│    │                                │               │
│    └─ Fallback (Adapter only) ──────┤               │
│        assessReport(peer)           │               │
│        assessReport(reporter)       │               │
│        (two separate API calls)     │               │
│                                     ▼               │
│    determineDecision() → approve / reject / escalate│
│    silentBanReporter() (if reporter was abusive)    │
└─────────────────────────────────────────────────────┘
```

## Model adapters

Each model has an adapter in `backend/golang/internal/automod/models/`. Adapters implement the `shared.Adapter` interface and optionally `shared.DualAssessmentAdapter`.

### Interface contracts

```go
// Required — every adapter must implement this.
type Adapter interface {
    Matches(model string) bool
    BuildPrompt(report storage.Report, peerEvidence string) string
    ParseAssessment(raw string) (Assessment, error)
}

// Optional — enables single-call dual assessment of both participants.
type DualAssessmentAdapter interface {
    BuildDualMessages(report storage.Report, reportedEvidence, reporterEvidence string) []CoreMessage
    ParseDualAssessment(raw string) (DualAssessment, error)
}
```

### Registered adapters

| Adapter | Model ID | Prompt format | Dual mode | Notes |
|---------|----------|---------------|-----------|-------|
| `generic` | `generic-json` | JSON (custom taxonomy) | JSON dual | Best for general-purpose LLMs (GPT, DeepSeek, Qwen, Claude) |
| `safetyguard8bv3` | `nvidia/llama-3.1-nemotron-safety-guard-8b-v3` | JSON (no report reason) | Native multi-message | Fine-tuned NIM safety classifier |
| `multilingualsafetyguard8bv1` | `nvidia/llama-3.1-nemotron-safety-guard-multilingual-8b-v1` | JSON (no report reason) | Native multi-message | Multilingual variant |
| `nemoguardcontentsafety8b` | `nvidia/llama-3.1-nemoguard-8b-content-safety` | JSON (no report reason) | Native multi-message | Content safety variant |
| `contentsafetyreasoning4b` | `nvidia/nemotron-content-safety-reasoning-4b` | Plaintext (prompt/response harm) | Plaintext dual | Chain-of-thought reasoning model (`/think` mode) |
| `llamaguard412b` | `meta/llama-guard-4-12b` | Plaintext (MLCommons taxonomy) | Two-call fallback | Uses its own fine-tuned taxonomy; rigid prompt format |

### Dual assessment modes

There are three dual assessment strategies, depending on the model:

**1. JSON dual prompt** (`generic` adapter)
- Sends a single `user` message containing both parties' evidence
- Asks for `{"Reported Safety": "...", "Reporter Safety": "..."}`
- Parsed by `shared.ParseDualJSONSafetyAssessment()`

**2. Native multi-message** (Nvidia safety guard models)
- Sends reported user evidence as `user` role, reporter evidence as `assistant` role
- Model responds with `{"User Safety": "...", "Response Safety": "..."}`
- Parsed by `shared.ParseNativeDualAssessment()`
- This matches the official NIM API format for these models

**3. Plaintext prompt/response harm** (`reasoning-4b`)
- Sends a single `user` message with reported evidence as "Human user" and reporter evidence as "AI assistant"
- Model responds with `Prompt harm: harmful/unharmful` and `Response harm: harmful/unharmful`
- Parsed by the adapter's own `ParseDualAssessment()` using regex

**4. Two-call fallback** (Llama Guard, or any adapter without `DualAssessmentAdapter`)
- Worker calls `assessReport()` twice: once for peer evidence, once for reporter evidence
- Each call is independent and uses the standard `BuildPrompt` / `ParseAssessment` path

## Adding a new model adapter

### Step 1: Create the adapter package

Create a new directory under `backend/golang/internal/automod/models/`:

```
backend/golang/internal/automod/models/your_model_name/
└── model.go
```

### Step 2: Implement the adapter

At minimum, implement `shared.Adapter`:

```go
package yourmodelname

import (
    "github.com/anish/omegle/backend/golang/internal/automod/models/shared"
    "github.com/anish/omegle/backend/golang/internal/storage"
)

const modelID = "provider/your-model-name"

type adapter struct{}

func New() adapter { return adapter{} }

func (adapter) ModelID() string { return modelID }

func (adapter) Matches(model string) bool {
    return shared.NormalizeModelID(model) == modelID
}

func (adapter) BuildPrompt(report storage.Report, peerEvidence string) string {
    // Build the prompt for single-user assessment.
    // Use shared.BuildJSONSafetyPrompt() if the model handles JSON,
    // or build a custom prompt matching the model's expected format.
    return shared.BuildJSONSafetyPrompt(report, peerEvidence, true)
}

func (adapter) ParseAssessment(raw string) (shared.Assessment, error) {
    // Parse the model's response into an Assessment.
    // Use shared.ParseJSONSafetyAssessment() for JSON responses,
    // shared.ParsePlaintextSafetyAssessment() for safe/unsafe text,
    // or implement custom parsing.
    return shared.ParseJSONSafetyAssessment(raw)
}
```

### Step 3: Add dual assessment support (optional)

If the model can assess both participants in a single call, implement `shared.DualAssessmentAdapter`:

```go
// For models that accept the standard JSON dual prompt:
func (adapter) BuildDualMessages(report storage.Report, reportedEvidence, reporterEvidence string) []shared.CoreMessage {
    return []shared.CoreMessage{
        {
            Role:    "user",
            Content: shared.BuildDualJSONSafetyPrompt(report, reportedEvidence, reporterEvidence, true),
        },
    }
}

func (adapter) ParseDualAssessment(raw string) (shared.DualAssessment, error) {
    return shared.ParseDualJSONSafetyAssessment(raw)
}
```

```go
// For models that use native user/assistant role classification:
func (adapter) BuildDualMessages(_ storage.Report, reportedEvidence, reporterEvidence string) []shared.CoreMessage {
    return shared.BuildNativeDualMessages(reportedEvidence, reporterEvidence)
}

func (adapter) ParseDualAssessment(raw string) (shared.DualAssessment, error) {
    return shared.ParseNativeDualAssessment(raw)
}
```

### Step 4: Register the adapter

Add your adapter to the registry in `backend/golang/internal/automod/models/registry.go`:

```go
import (
    yourmodelname "github.com/anish/omegle/backend/golang/internal/automod/models/your_model_name"
)

var registeredAdapters = []shared.Adapter{
    // ... existing adapters ...
    yourmodelname.New(),
    generic.New(), // generic must stay last (catch-all)
}
```

> **Important:** The `generic` adapter should remain last in the list since it matches `generic-json` by model type, not by model ID prefix.

### Step 5: Update ENVIRONMENT.md

Add your model's ID to the `AUTO_MODERATION_MODEL_TYPE` list in `ENVIRONMENT.md`.

## Shared prompt/parser helpers

The `shared` package provides reusable building blocks:

| Helper | Purpose |
|--------|---------|
| `BuildJSONSafetyPrompt()` | Single-user JSON prompt with the platform's safety taxonomy |
| `ParseJSONSafetyAssessment()` | Parses `{"User Safety": "...", "Safety Categories": "..."}` |
| `BuildDualJSONSafetyPrompt()` | Dual-user JSON prompt (Reported + Reporter) |
| `ParseDualJSONSafetyAssessment()` | Parses `{"Reported Safety": "...", "Reporter Safety": "..."}` |
| `BuildNativeDualMessages()` | Builds `user`/`assistant` role messages for native dual models |
| `ParseNativeDualAssessment()` | Parses `{"User Safety": "...", "Response Safety": "..."}` |
| `ParsePlaintextSafetyAssessment()` | Parses `safe`/`unsafe` text followed by category lines |
| `NormalizeCategories()` | Splits comma-separated category strings |
| `SanitizePromptText()` | Strips newlines and truncates user-supplied text for safe prompt injection |

## Safety taxonomy

The platform taxonomy is defined in `backend/golang/internal/automod/taxonomy/taxonomy.go` with 23 categories (S1–S23). Models that were fine-tuned on different taxonomies (e.g., Llama Guard's MLCommons taxonomy) have their own category mapping in their adapter.

## Counter-ban behavior

When the system detects that the **reporter** also sent abusive messages, it applies a "counter-ban" — a silent 7-day temporary ban on the reporter's session ID and IP. This prevents abuse of the report system by users who are themselves violating terms. The counter-ban is applied regardless of the decision on the reported user.

## Credits

Obviously myself for now. This was by far the easiest thing to add in this project!

I have extensive experience bringing up ROCm-based cards back in 2022-2024. We maintained our own `torch-xla` fork with AMD card support, which (at least at the time) was rare to see. We worked across TensorFlow, PyTorch, XLA, and various other technologies to integrate AMD APIs. 

In some releases, we also added opportunistic memory allocation. If a model was very large, we offloaded parts of it between system RAM and VRAM. The caveat is that it was slow, so it remained primarily a research and development feature. Why was this special when TensorFlow already had something similar? TensorFlow uses the native CUDA/ROCm allocator, which naïvely splits allocations between RAM and VRAM, drastically impacting performance. An optimal approach should completely exhaust all available VRAM first—leaving just a few hundred megabytes for GNOME or other essential desktop apps—before falling back to system RAM. Achieving this requires extremely fine-grained memory management in C++ and dependable hardware cache coherence.

You can check out [huggingface.co/aless2212](https://huggingface.co/aless2212), where we uploaded various models in ONNX format running on an 8GB card in FP16 precision. This served as a proof-of-concept, especially since CPUs typically encode in FP32 rather than FP16. If needed, we could absolutely build a proper local pipeline here with everything set up from scratch.

However, running local models is simply not worth the infrastructure overhead at our current scale when API tokens are so cheap for this kind of task.

We coded all of that infrastructure back then without the help of LLMs. Today, the main challenge we face is "Prompt Injection." Usually, major LLM providers run every request through dedicated decoder guard models (like Llama-Guard or Nemotron-Content-Safety), ensuring high levels of sanitization. This is another reason why relying on AI providers is much more feasible at a low scale: they add built-in prompt guards or enforce strict schema alignments by default. While jailbreaks certainly still exist—and if a model's fine-tuning can't reject a cleverly crafted prompt, it becomes a vulnerability—the tradeoff is overall highly favorable for reducing the human moderation burden.