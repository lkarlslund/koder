# Model overlays

Model overlays describe model-specific settings without hardcoding controls or
request fields in Koder. Built-in overlays are installed in
`~/.koder/assets/model-overlays/`. You can edit an installed file or add another
JSON file to that directory. Restart Koder after changing an overlay.

An overlay contains model-name globs, UI controls, and bindings from each
control to the outgoing OpenAI-compatible request body:

```json
{
  "version": 1,
  "id": "example-model",
  "title": "Example model",
  "priority": 500,
  "match": {
    "model_ids": ["*example-model*"],
    "transports": ["llama"]
  },
  "controls": [
    {
      "id": "reasoning_level",
      "label": "Reasoning level",
      "help": "Reasoning levels accepted by this model.",
      "type": "select",
      "default": "auto",
      "choices": [
        {"value": "auto", "label": "Server default"},
        {"value": "low", "label": "Low"},
        {"value": "high", "label": "High"}
      ],
      "bindings": [
        {
          "path": "chat_template_kwargs.reasoning_level",
          "transports": ["llama"],
          "omit_values": ["auto"]
        }
      ]
    }
  ]
}
```

Supported control types are `select`, `number`, `text`, `checkbox`, and
`hidden`. Number controls can specify `min`, `max`, and `step`; text and number
controls can specify `placeholder`. A binding supports:

- `path`: dot-separated request-body path.
- `transports`: optional list containing `llama`, `dashscope`, or `openai`.
- `omit_values`: values that should use the server default instead.
- `value_map`: maps UI values to request values, such as `"enabled": true`.

The generic overlay is always applied first. In auto mode, matching overlays
are then applied in priority order, and a later control with the same ID
replaces the earlier definition. Selecting “Generic only” disables automatic
model-specific matching. Custom request JSON is applied last and can override
overlay-generated request fields except protected chat protocol fields.

Installed built-ins are managed defaults: unmodified files receive updates,
while locally edited files are preserved. A user file with the same filename as
a built-in replaces that built-in when the catalog is loaded.
