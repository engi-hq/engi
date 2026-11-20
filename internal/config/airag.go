package config

import (
	"fmt"
	"time"
)

// AIRAGConfig tunes the retrieval nodes: embeddings and the pgvector store.
type AIRAGConfig struct {
	// OpenAIEmbeddingModel and OllamaEmbeddingModel apply when an embeddings
	// node leaves the model empty (Azure always needs a deployment name;
	// Anthropic has no embeddings API).
	OpenAIEmbeddingModel string `yaml:"openaiEmbeddingModel"`
	OllamaEmbeddingModel string `yaml:"ollamaEmbeddingModel"`
	// EmbeddingBatchSize is how many texts one embeddings request carries.
	EmbeddingBatchSize int `yaml:"embeddingBatchSize"`
	// EmbeddingRequestTimeout bounds one embeddings request (one batch).
	EmbeddingRequestTimeout time.Duration `yaml:"embeddingRequestTimeout"`
	// MaxTopK caps the results of one vector search.
	MaxTopK int `yaml:"maxTopK"`
	// InsertBatchSize is how many rows one INSERT statement carries.
	InsertBatchSize int `yaml:"insertBatchSize"`
	// MaxDeleteIDs caps the ids of one vector store delete.
	MaxDeleteIDs int `yaml:"maxDeleteIds"`
}

func defaultAIRAG() AIRAGConfig {
	return AIRAGConfig{
		OpenAIEmbeddingModel: "text-embedding-3-small", OllamaEmbeddingModel: "nomic-embed-text",
		EmbeddingBatchSize: 64, EmbeddingRequestTimeout: time.Minute,
		MaxTopK: 100, InsertBatchSize: 100, MaxDeleteIDs: 1000,
	}
}

func (c AIRAGConfig) validate(errs []error) []error {
	for name, v := range map[string]string{
		"nodes.ai.rag.openaiEmbeddingModel": c.OpenAIEmbeddingModel, "nodes.ai.rag.ollamaEmbeddingModel": c.OllamaEmbeddingModel,
	} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s must not be empty", name))
		}
	}
	errs = appendPositive(errs, "nodes.ai.rag.embeddingBatchSize", int64(c.EmbeddingBatchSize))
	errs = appendPositive(errs, "nodes.ai.rag.embeddingRequestTimeout", int64(c.EmbeddingRequestTimeout))
	errs = appendPositive(errs, "nodes.ai.rag.maxTopK", int64(c.MaxTopK))
	errs = appendPositive(errs, "nodes.ai.rag.insertBatchSize", int64(c.InsertBatchSize))
	return appendPositive(errs, "nodes.ai.rag.maxDeleteIds", int64(c.MaxDeleteIDs))
}

func aiRAGSettings() []setting {
	return []setting{
		{"ENGI_NODES_AI_RAG_OPENAI_EMBEDDING_MODEL", "nodes-ai-rag-openai-embedding-model", "OpenAI embedding model used when a node sets none",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.RAG.OpenAIEmbeddingModel })},
		{"ENGI_NODES_AI_RAG_OLLAMA_EMBEDDING_MODEL", "nodes-ai-rag-ollama-embedding-model", "Ollama embedding model used when a node sets none",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.RAG.OllamaEmbeddingModel })},
		{"ENGI_NODES_AI_RAG_EMBEDDING_BATCH_SIZE", "nodes-ai-rag-embedding-batch-size", "texts per embeddings request",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.RAG.EmbeddingBatchSize })},
		{"ENGI_NODES_AI_RAG_EMBEDDING_REQUEST_TIMEOUT", "nodes-ai-rag-embedding-request-timeout", "time limit of one embeddings request",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.RAG.EmbeddingRequestTimeout })},
		{"ENGI_NODES_AI_RAG_MAX_TOP_K", "nodes-ai-rag-max-top-k", "largest topK of a vector search",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.RAG.MaxTopK })},
		{"ENGI_NODES_AI_RAG_INSERT_BATCH_SIZE", "nodes-ai-rag-insert-batch-size", "rows per vector store INSERT statement",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.RAG.InsertBatchSize })},
		{"ENGI_NODES_AI_RAG_MAX_DELETE_IDS", "nodes-ai-rag-max-delete-ids", "most ids one vector store delete accepts",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.RAG.MaxDeleteIDs })},
	}
}
