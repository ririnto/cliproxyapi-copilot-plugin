# Provider State Conversions

Native Messages and Responses routes preserve hosted tools, provider-owned references and signed thinking in their original protocol.
The provider remains responsible for validating ownership and model capabilities.
The bridge cannot transfer that ownership by renaming JSON fields.

## Cross-Protocol Boundaries

Client function declarations, calls and results continue through the existing supported codecs.
Hosted declarations, server tool calls, hosted results, unresolved response references and unsupported custom tools return typed conversion errors before inference.
Error-bearing Messages tool results and hosted caller metadata also return an error when the destination adapter cannot represent their meaning.
The bridge does not turn a server tool call into a client function.
A one-way request mapping does not establish a complete hosted execution conversion.

Native signed or encrypted thinking is rejected on incompatible protocol edges.
Plain reasoning summaries keep the existing conversion behavior.
Authenticated Copilot Chat replay carriers retain their separate account, model, route and history checks.
The bridge does not remove thinking blocks to obtain a successful conversion.

Cross-protocol responses and stream events reject unrepresented hosted items, signed thinking, citations and hosted pause state.
This prevents a successful result or terminal event after silently losing provider state.
Some earlier stream text may already have been delivered before a later incompatible event fails the stream.
Same-protocol response bodies and stream frames remain native pass-through.

`ErrProviderStateConversion` and `ProviderStateConversionError` expose structured source, destination, kind and field identifiers.
Request execution maps this failure to `unsupported_provider_state` and HTTP 422 before inference.
Response translation retains the executor's HTTP 502 translation-error boundary, while streaming reports a terminal translation failure.
Errors describe unsupported shapes without embedding state contents.

Generic cross-provider hosted execution adapters, state resolvers and provenance snapshots belong in CLIProxyAPI.
Live hosted execution, native signed continuation, citations and original-client qualification are NOT RUN in [Issue #66](https://github.com/ririnto/cpa-copilot-bridge/issues/66).
