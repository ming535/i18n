package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"

	openai "github.com/sashabaranov/go-openai"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
)

// FlattenedJSON represents the flattened structure of the JSON file
type FlattenedJSON map[string]string

// KeyUsage represents the usage of a key in a file
type KeyUsage struct {
	File     string `json:"file"`
	Function string `json:"function"`
}

type KeyTranslation struct {
	Original         string
	File             string
	Function         string
	AIContext        string
	Tr               string
	TrWithFunContext string
	TrWithAIContext  string
}

func main() {
	// Read and parse the en.json file
	enJsonPath := filepath.Join("web", "messages", "en.json")
	enJson, err := readJSON(enJsonPath)
	if err != nil {
		fmt.Printf("Error reading JSON: %v\n", err)
		return
	}

	// Flatten the JSON object
	flattenedPairs := flattenJSON(enJson, "")

	// Find all TSX files in the web directory
	webDir := filepath.Join("web")
	tsxFiles, err := findTSXFiles(webDir)
	if err != nil {
		fmt.Printf("Error finding TSX files: %v\n", err)
		return
	}

	// Print all TSX files
	fmt.Println("TSX files found:")
	for _, file := range tsxFiles {
		fmt.Println(file)
	}
	fmt.Println() // Add a blank line for better readability

	// Initialize tree-sitter parser
	parser := sitter.NewParser()
	parser.SetLanguage(tsx.GetLanguage())

	// Process files and find key usage
	keyUsage, err := findKeyUsage(flattenedPairs, tsxFiles, parser)
	if err != nil {
		fmt.Printf("Error finding key usage: %v\n", err)
		return
	}

	// print the key usage
	// for key, usage := range keyUsage {
	// 	fmt.Printf("Key: %s, File: %s, Function: \n%s\n\n", key, usage.File, usage.Function)
	// }

	keyTranslations := make(map[string]KeyTranslation)
	for key, usage := range keyUsage {
		keyTranslations[key] = KeyTranslation{
			Original: flattenedPairs[key],
			File:     usage.File,
			Function: usage.Function,
			Tr:       flattenedPairs[key],
		}
	}

	// Initialize OpenAI client
	config := openai.DefaultConfig(os.Getenv("OPENROUTER_API_KEY"))
	config.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(config)

	// Translate each key using OpenAI's API
	var wg sync.WaitGroup
	results := make(chan string, len(keyTranslations))

	for key, translation := range keyTranslations {
		wg.Add(1)
		go func(key string, translation KeyTranslation) {
			defer wg.Done()

			err := simpleTranslate(key, &translation, client)
			if err != nil {
				results <- fmt.Sprintf("Error translating key %s: %v\n", key, err)
				return
			}

			err = translateWithFunContext(key, &translation, client)
			if err != nil {
				results <- fmt.Sprintf("Error translating key %s: %v\n", key, err)
				return
			}

			err = translateWithAIContext(key, &translation, client)
			if err != nil {
				results <- fmt.Sprintf("Error translating key %s: %v\n", key, err)
				return
			}

			results <- fmt.Sprintf("Key: %s\nOriginal: %s\nSimple Translation: %s\nFunc Translation: %s\nContext Translation: %s\nAI Context: %s\n\n",
				key, translation.Original, translation.Tr, translation.TrWithFunContext, translation.TrWithAIContext, translation.AIContext)
		}(key, translation)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		fmt.Print(result)
	}

}

func simpleTranslate(key string, translation *KeyTranslation, client *openai.Client) error {
	prompt := fmt.Sprintf(`
	You are a translation expert that translate website.
	Please translate the following English text to Chinese in a website.
	When doing the transltion, please only returns the translation, no other text.
	---
	Text to translate: %s`, translation.Original)
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)

	if err != nil {
		fmt.Printf("Error translating key %s: %v\n", key, err)
		return err
	}

	if len(resp.Choices) > 0 {
		translation.Tr = resp.Choices[0].Message.Content
		return nil
	} else {
		return fmt.Errorf("no translation found for key %s", key)
	}
}

func translateWithFunContext(key string, translation *KeyTranslation, client *openai.Client) error {
	prompt := fmt.Sprintf(`
	You are a translation expert that translate website.
	Please translate the following English text to Chinese, considering the function context.
	The function context is the code that contains the user facing text to translate.
	Inside the function context, the text to translate is wrapped with "---" and "---".
	Please only returns the translation, no other text.
	---
	Function context: %s
	---
	Text to translate: %s.
	`, translation.Function, translation.Original)
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)
	if err != nil {
		fmt.Printf("Error translating key %s: %v\n", key, err)
		return err
	}

	if len(resp.Choices) > 0 {
		translation.TrWithFunContext = resp.Choices[0].Message.Content
		return nil
	} else {
		return fmt.Errorf("no translation found for key %s", key)
	}
}

func translateWithAIContext(key string, translation *KeyTranslation, client *openai.Client) error {
	// returns the readable context from function
	prompt := fmt.Sprintf(`
	I have some code, it contains user facing string I want to translate.
	Please extract the human readable tranlsation context about the string based on the code I provide so that I can use it to translate the string by translators.
	Inside the code, the text to translate is wrapped with "---" and "---".
	The translation context should describes the context around the text to translate which is wrapped with "---" and "---".
	The translation context should only describes the UI/UX where the string is used.
	The translation context should not mention that it is related to code.
	The translation context should not mention the "---" and "---" wrapper.
	The translation context should be easy for translators to understand.
	Please start with "It appears".
	---
	Code: %s,	
	`, translation.Function)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)
	if err != nil {
		fmt.Printf("Error translating key %s: %v\n", key, err)
		return err
	}
	if len(resp.Choices) > 0 {
		translation.AIContext = resp.Choices[0].Message.Content
	}

	// translate based on AI context
	prompt = fmt.Sprintf(`
	You are a translation expert that translate website's UI.
	Please translate the following English text to Chinese.
	When doing the translation, please consider the translation context I provide and make the translation more accurate and natural.
	When doing the translation, please take into account that this is a website's UI, please use the most popular and widely used words and expressions.
	If there are some words or expressions that is widely understood an do not need translation, please do not translate them.
	Do not translate "Bug".
	---
	Translation Context: %s
	---
	Text to translate: %s.
	`, translation.AIContext, translation.Original)
	resp, err = client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			Temperature: 0,
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

func readJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	return result, err
}

func flattenJSON(obj map[string]interface{}, prefix string) FlattenedJSON {
	flattened := make(FlattenedJSON)
	for k, v := range obj {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}

		switch child := v.(type) {
		case map[string]interface{}:
			for ck, cv := range flattenJSON(child, newKey) {
				flattened[ck] = cv
			}
		default:
			flattened[newKey] = fmt.Sprintf("%v", v)
		}
	}
	return flattened
}

func findTSXFiles(dir string) ([]string, error) {
	gitignore, err := ignore.CompileIgnoreFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		// If .gitignore doesn't exist, continue without ignoring
		gitignore = ignore.CompileIgnoreLines()
	}

	var files []string
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(dir, path)
		if !info.IsDir() && strings.HasSuffix(path, ".tsx") && !gitignore.MatchesPath(relPath) {
			// print the file name
			// fmt.Printf("Found TSX file: %s in directory: %s\n", filepath.Base(path), filepath.Dir(path))
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func findKeyUsage(flattenedKeys FlattenedJSON, tsxFiles []string, parser *sitter.Parser) (map[string]KeyUsage, error) {
	results := make(map[string]KeyUsage)

	// Create the query once, outside the loops
	query, err := sitter.NewQuery([]byte(`
		(call_expression
			function: (identifier) @func
			arguments: (arguments
				(string (string_fragment) @key)
				.
			)
		)
	`), tsx.GetLanguage())
	if err != nil {
		return nil, err
	}

	for _, file := range tsxFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}

		tree, err := parser.ParseCtx(context.Background(), nil, content)
		if err != nil {
			return nil, err
		}

		qc := sitter.NewQueryCursor()
		qc.Exec(query, tree.RootNode())

		for {
			match, ok := qc.NextMatch()
			if !ok {
				break
			}

			for _, capture := range match.Captures {
				if capture.Node.Type() == "identifier" && capture.Node.Content(content) == "t" {
					keyNode := match.Captures[1].Node
					keyContent := keyNode.Content(content)
					parent := findParentFunction(capture.Node)
					if parent == nil {
						continue
					}
					parentFunctionName := getFunctionName(parent, content)

					fullKeyName := fmt.Sprintf("%s.%s", parentFunctionName, keyContent)
					parentFunctionContent := parent.Content(content)

					for key := range flattenedKeys {
						if fullKeyName == key {
							// replace the keyContent inside parentFunctionContent with value
							replacedContent := strings.Replace(parentFunctionContent, keyContent, fmt.Sprintf("---%s---", flattenedKeys[key]), -1)
							results[key] = KeyUsage{File: file, Function: replacedContent}
						}
					}
				}
			}
		}
	}

	return results, nil
}

func findParentFunction(node *sitter.Node) *sitter.Node {
	for node != nil {
		if node.Type() == "function_declaration" || node.Type() == "arrow_function" || node.Type() == "method_definition" {
			return node
		}
		node = node.Parent()
	}
	return nil
}

func getFunctionName(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}

	switch node.Type() {
	case "function_declaration":
		// For function declarations, the name is the second child
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			return nameNode.Content(content)
		}
	case "method_definition":
		// For method definitions, the name is also the second child
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			return nameNode.Content(content)
		}
	case "arrow_function":
		// Arrow functions might be anonymous, so we need to check the parent
		if parent := node.Parent(); parent != nil {
			switch parent.Type() {
			case "variable_declarator":
				// If it's assigned to a variable, use the variable name
				if nameNode := parent.ChildByFieldName("name"); nameNode != nil {
					return nameNode.Content(content)
				}
			case "pair":
				// If it's in an object literal, use the key name
				if keyNode := parent.ChildByFieldName("key"); keyNode != nil {
					return keyNode.Content(content)
				}
			}
		}
	}

	// If we couldn't determine the name, return an empty string
	return ""
}
