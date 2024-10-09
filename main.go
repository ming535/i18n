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
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/tidwall/gjson"
)

type OrderedKV struct {
	Key   string
	Value gjson.Result
}

func parseOrderedJSON(jsonStr string) []OrderedKV {
	var result []OrderedKV
	gjson.Parse(jsonStr).ForEach(func(key, value gjson.Result) bool {
		result = append(result, OrderedKV{Key: key.String(), Value: value})
		return true
	})
	return result
}

type KeyTranslation struct {
	FlattenedKey     string
	Text             string
	UsageFound       bool
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
	kvArray := flattenJSON(parseOrderedJSON(string(enJson)), "")

	// Find all TSX files in the web directory
	webDir := filepath.Join("web")
	files, err := findTSFiles(webDir)
	if err != nil {
		fmt.Printf("Error finding TSX files: %v\n", err)
		return
	}

	// Print all TSX files
	// fmt.Println("Files found:")
	// for _, file := range files {
	// 	fmt.Println(file)
	// }
	fmt.Println() // Add a blank line for better readability

	// Initialize tree-sitter tsxParser
	tsxParser := sitter.NewParser()
	tsxParser.SetLanguage(tsx.GetLanguage())

	tsParser := sitter.NewParser()
	tsParser.SetLanguage(typescript.GetLanguage())

	keyTranslations, err := createTrContext(kvArray, files, tsxParser, tsParser)
	if err != nil {
		fmt.Printf("Error finding key usage: %v\n", err)
		return
	}

	// print the key usage
	// for key, usage := range keyUsage {
	// 	fmt.Printf("Key: %s, File: %s, Function: \n%s\n\n", key, usage.File, usage.Function)
	// }

	// Initialize OpenAI client
	config := openai.DefaultConfig(os.Getenv("OPENROUTER_API_KEY"))
	config.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(config)

	// Translate each key using OpenAI's API
	var wg sync.WaitGroup
	results := make(chan string, len(keyTranslations))

	for i, translation := range keyTranslations {
		wg.Add(1)
		go func(i int, translation KeyTranslation) {
			defer wg.Done()

			err := simpleTranslate(keyTranslations[i].FlattenedKey, &translation, client)
			if err != nil {
				results <- fmt.Sprintf("Error translating key %s: %v\n", keyTranslations[i].FlattenedKey, err)
				return
			}

			if translation.UsageFound {
				err = translateWithFunContext(keyTranslations[i].FlattenedKey, &translation, client)
				if err != nil {
					results <- fmt.Sprintf("Error translating key %s: %v\n", keyTranslations[i].FlattenedKey, err)
					return
				}

				err = TranslateWithAIContext(keyTranslations[i].FlattenedKey, &translation, client)
				if err != nil {
					results <- fmt.Sprintf("Error translating key %s: %v\n", keyTranslations[i].FlattenedKey, err)
					return
				}
			}

			results <- fmt.Sprintf("Key: %s\nText: %s\nSimple Translation: %s\nUsageFound: %t\nFunc Translation: %s\nContext Translation: %s\nAI Context: %s\n\n",
				keyTranslations[i].FlattenedKey, translation.Text, translation.Tr, translation.UsageFound, translation.TrWithFunContext, translation.TrWithAIContext, translation.AIContext)
		}(i, translation)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		fmt.Print(result)
	}

	err = serializeToJSON(keyTranslations)
	if err != nil {
		fmt.Printf("Error serializing to JSON: %v\n", err)
		return
	}

}

func serializeToJSON(keyTranslations []KeyTranslation) error {
	// Serialize keyTranslations to JSON
	jsonOutput := make(map[string]interface{})
	for _, translation := range keyTranslations {
		keys := strings.Split(translation.FlattenedKey, ".")
		current := jsonOutput
		for i, key := range keys {
			if i == len(keys)-1 {
				// Last key, set the value
				current[key] = translation.Tr
				// current[key] = map[string]interface{}{
				// 	"text":               translation.Text,
				// 	"simpleTranslation":  translation.Tr,
				// 	"usageFound":         translation.UsageFound,
				// 	"funcTranslation":    translation.TrWithFunContext,
				// 	"contextTranslation": translation.TrWithAIContext,
				// 	"aiContext":          translation.AIContext,
				// }
			} else {
				// Not the last key, create nested map if it doesn't exist
				if _, exists := current[key]; !exists {
					current[key] = make(map[string]interface{})
				}
				current = current[key].(map[string]interface{})
			}
		}
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(jsonOutput, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling to JSON: %v\n", err)
		return err
	}

	// Write JSON to file
	err = os.WriteFile("translations.json", jsonData, 0644)
	if err != nil {
		fmt.Printf("Error writing JSON to file: %v\n", err)
		return err
	}

	fmt.Println("Translations have been serialized to translations.json")
	return nil
}

func simpleTranslate(key string, translation *KeyTranslation, client *openai.Client) error {

	systemPrompt := `
	You are a translation assistant for a website. You are responsible for translate the website's UI.
	You will receive a language code and a text delimited by "---" that needs to be translated.
	Please follow the steps below to process the code snippet:
		Step 1: Infer the regional language corresponding to the language code you received.
		Step 2: Translate the text delimited by "---" into the corresponding regional language.
	Ensure the output can be directly used in the website.
	Ensure the output does not contain any placeholders like "---".
	`

	userPrompt := fmt.Sprintf(`
	Language code: %s
	Text to translate: ---%s---
	`, `zh-CN`, translation.Text)

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
		translation.Tr = resp.Choices[0].Message.Content
		return nil
	} else {
		return fmt.Errorf("no translation found for key %s", key)
	}
}

func translateWithFunContext(key string, translation *KeyTranslation, client *openai.Client) error {
	systemPrompt := `
	You are a translation assistant for a website. You are responsible for translate the website's UI.
	You will receive a language code, a code snippet, a text delimited by "---" that needs to be translated.
	Please follow the steps below to process the code snippet:
		Step 1: Infer the regional language corresponding to the language code you received.
		Step 2: Infer the UI related context on how the text delimited by "---" is used from the code snippet.
		Step 3: Translate the text delimited by "---" into the corresponding regional language, based on the UI related context.
	Ensure the output can be directly used in the website.
	Ensure the output does not contain any placeholders like "---".
	`

	userPrompt := fmt.Sprintf(`
	Language code: %s
	Code snippet: %s
	Text to translate: ---%s---
	`, `zh-CN`, translation.Function, translation.Text)

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
		translation.TrWithFunContext = resp.Choices[0].Message.Content
		return nil
	} else {
		return fmt.Errorf("no translation found for key %s", key)
	}
}

func readJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func flattenJSON(parsed []OrderedKV, prefix string) []OrderedKV {
	var flattened []OrderedKV
	for _, kv := range parsed {
		newKey := kv.Key
		if prefix != "" {
			newKey = prefix + "." + kv.Key
		}

		if kv.Value.IsObject() {
			childFlattened := flattenJSON(parseOrderedJSON(kv.Value.Raw), newKey)
			flattened = append(flattened, childFlattened...)
		} else {
			flattened = append(flattened, OrderedKV{Key: newKey, Value: kv.Value})
		}
	}
	return flattened
}
func findTSFiles(dir string) ([]string, error) {
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
		if !info.IsDir() && (strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".ts")) && !gitignore.MatchesPath(relPath) {
			// print the file name
			// fmt.Printf("Found TSX file: %s in directory: %s\n", filepath.Base(path), filepath.Dir(path))
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func createTrContext(flattenedKVs []OrderedKV, tsxFiles []string, tsxParser *sitter.Parser, tsParser *sitter.Parser) ([]KeyTranslation, error) {
	results := make([]KeyTranslation, len(flattenedKVs))

	// Initialize all keys with Found set to false
	for i := range flattenedKVs {
		results[i] = KeyTranslation{FlattenedKey: flattenedKVs[i].Key, Text: flattenedKVs[i].Value.String(), UsageFound: false}
	}

	// Create the tsxQuery once, outside the loops
	tsxQuery, err := sitter.NewQuery([]byte(`
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

	tsQuery, err := sitter.NewQuery([]byte(`
		(call_expression
			function: (identifier) @func
			arguments: (arguments
				(string (string_fragment) @key)
				.
			)
		)
	`), typescript.GetLanguage())
	if err != nil {
		return nil, err
	}

	for _, file := range tsxFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}

		var tree *sitter.Tree
		if strings.HasSuffix(file, ".tsx") {
			tree, err = tsxParser.ParseCtx(context.Background(), nil, content)
		} else {
			tree, err = tsParser.ParseCtx(context.Background(), nil, content)
		}
		if err != nil {
			return nil, err
		}

		qc := sitter.NewQueryCursor()
		if strings.HasSuffix(file, ".tsx") {
			qc.Exec(tsxQuery, tree.RootNode())
		} else {
			qc.Exec(tsQuery, tree.RootNode())
		}

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

					for i := range flattenedKVs {
						flattenedKey := flattenedKVs[i].Key
						if fullKeyName == flattenedKey {
							// replace the keyContent inside parentFunctionContent with value
							replacedContent := strings.Replace(parentFunctionContent, keyContent, fmt.Sprintf("---%s---", flattenedKVs[i].Value.String()), -1)
							results[i] = KeyTranslation{FlattenedKey: flattenedKey, Text: flattenedKVs[i].Value.String(), UsageFound: true, File: file, Function: replacedContent}
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
