package config

// FileUploadConfig controls how user-uploaded files are forwarded to a provider.
// Native MIME types may be sent as provider-native file/document parts when the
// provider implementation supports them. Text-embed MIME types may be converted
// to ordinary prompt text as a portable fallback.
type FileUploadConfig struct {
	NativeMimeTypes    []string `mapstructure:"native_mime_types" yaml:"native_mime_types,omitempty"`
	MaxNativeBytes     int64    `mapstructure:"max_native_bytes" yaml:"max_native_bytes,omitempty"`
	TextEmbedMimeTypes []string `mapstructure:"text_embed_mime_types" yaml:"text_embed_mime_types,omitempty"`
	MaxTextEmbedBytes  int64    `mapstructure:"max_text_embed_bytes" yaml:"max_text_embed_bytes,omitempty"`
}
