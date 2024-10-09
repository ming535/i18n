package main

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

func inferAIContext(key string, function string, client *openai.Client) (string, error) {
	systemPrompt := `
	You are a translation assistant for a website. You are responsible for extracting the UI related context from the code snippet.
	You will receive a code snippet, and a text delimited by "---" that needs to be translated later.
	Please follow the steps below to process the code snippet:
		Step 1: Infer the UI related context on how the text delimited by "---" is used from the code snippet.
		Step 2: Return the UI related context using one or two sentences.
	Ensure the output can be directly later by a translator later to translate the text delimited by "---".
	Ensure the output only describes what the UI element looks like, without mentioning any specific technology or code.
	Ensure the output does not contain any placeholders like "---".
	`

	userPrompt := fmt.Sprintf(`
	Code snippet: %s
	Text will be translated later: ---%s---
	`, function, key)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:       openai.GPT4oMini,
			Temperature: 0,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userPrompt,
				},
			},
		},
	)
	if err != nil {
		fmt.Printf("Error inferring AI context for key %s: %v\n", key, err)
		return "", err
	}
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content, nil
	} else {
		return "", fmt.Errorf("no AI context found for key %s", key)
	}
}

func translate(key string, translation *KeyTranslation, client *openai.Client) error {
	systemPrompt := `
	You are a translation assistant for a website. You are responsible for translate the website's UI based on the translation context provided.
	You will receive a language code, a translation context, and a text delimited by "---" that needs to be translated.
	Please follow the steps below to process the text delimited by "---":
		Step 1: Infer the regional language corresponding to the language code you received.
		Step 2: Translate the text delimited by "---" into the corresponding regional language.
	If you are not confident with the translation, please using the translation context to help you understand the meaning of the text needs to be translated.

	Ensure the output can be directly used in the website's UI elements.
	Ensure the output does not contain any placeholders like "---".
	Ensure the output does not contain any translation context provided.
	Ensure the output adheres to the translation context provided.
	`

	userPrompt := fmt.Sprintf(`
	Language code: %s
	Translation context: %s
	Text to translate: ---%s---
	`, `zh-CN`, translation.AIContext, translation.Text)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:       openai.GPT4oMini,
			Temperature: 0,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userPrompt,
				},
			},
		},
	)
	if err != nil {
		fmt.Printf("Error translating key %s: %v\n", key, err)
		return err
	}
	if len(resp.Choices) > 0 {
		translation.TrWithAIContext = resp.Choices[0].Message.Content
		return nil
	} else {
		return fmt.Errorf("no translation found for key %s", key)
	}
}

func TranslateWithAIContext(key string, translation *KeyTranslation, client *openai.Client) error {
	aiContext, err := inferAIContext(key, translation.Function, client)
	if err != nil {
		return err
	}
	translation.AIContext = aiContext
	return translate(key, translation, client)
}
