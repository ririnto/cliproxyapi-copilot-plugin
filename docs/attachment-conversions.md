# Attachment Conversions

The bridge preserves supported attachment fields and rejects cross-protocol routes that would lose their meaning.
It reuses the bundled CLIProxyAPI codecs and the existing Messages-to-Responses document adapter.
Document-bearing tool results use the same document adapter as ordinary message content.

## Current Boundaries

| Client and provider edge | Offline contract |
| --- | --- |
| Messages to Messages | Native document sources, ownership fields and metadata retain their native shape. |
| Responses to Responses | Native file references, filenames and image detail retain their native shape. |
| Messages to Responses | Images and document sources map to their Responses content fields, including documents inside tool results. |
| Messages to Chat | Supported image content is retained, while document conversion returns an unsupported attachment error. |
| Responses to Messages | Images without detail hints and inline PDFs with an explicit PDF data URI are retained. |
| Responses to Chat | Supported image URLs and `auto`, `low` or `high` detail are retained, while file conversion returns an unsupported attachment error. |

Messages document titles retain their exact whitespace when mapped to Responses filenames.
The reverse Messages file conversion cannot represent a Responses filename, document URL or provider-owned file ID through the bundled codec.
Those fields return an unsupported attachment error rather than disappearing.
Unrepresented metadata, provider-owned image sources, incompatible image detail and unsupported audio or video conversion also return that error.
Native protocol routes retain their fields and rely on the provider for capability and ownership validation.

The library exposes `ErrAttachmentConversion` and `AttachmentConversionError` with structured source, destination, kind and field identifiers.
The executor returns `unsupported_attachment` with HTTP 422 before JSON or streaming inference dispatch.
The error identifies the unsupported field without including file bytes or URLs.

## Ownership And Qualification

Additional wire representations, media preparation and document transcoding belong in the generic proxy translators.
The plugin does not stringify unsupported media, invent file ownership or add a parallel general converter.
Support for a native file reference does not make that reference portable to another provider protocol.

Synthetic structured tests prove field preservation and rejected conversion paths.
They do not establish model acceptance of PDF, URL or tool-result media.
Live Copilot attachment inference and original-client acceptance remain NOT RUN in [Issue #62](https://github.com/ririnto/cpa-copilot-bridge/issues/62).
