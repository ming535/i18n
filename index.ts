#! /usr/bin/env bun

import { readFileSync, writeFileSync } from "fs";
import * as ai from "ai";
import { z } from "zod";
import { registry } from "./registry";

function loadJSON(filePath: string): any {
  const file = readFileSync(filePath, "utf8");
  return JSON.parse(file);
}

function saveJSON(filePath: string, data: any) {
  writeFileSync(filePath, JSON.stringify(data, null, 2), "utf8");
}

function iterateObject(obj: any, path: string[] = []): Promise<TrResult>[] {
  const promises: Promise<TrResult>[] = [];

  for (const [key, value] of Object.entries(obj)) {
    const currentPath = [...path, key];
    if (typeof value === "string") {
      promises.push(
        (async () => {
          return await processKV(currentPath, value);
        })()
      );
    } else if (typeof value === "object" && value !== null) {
      promises.push(...iterateObject(value, currentPath));
    }
  }

  return promises;
}

type TrResult = {
  keys: string[];
  value: string;
  simple: Translation;
  func?: Translation;
  aiCtx?: Translation;
};

type Translation = {
  tr: string;
  score: number;
};

const trModel = "openai:gpt-4o-mini";
const reviewMode = "openai:gpt-4o";

async function processKV(keys: string[], value: string): Promise<TrResult> {
  const tr = await trSimple({ keys, value, langCode: "zh-CN" });
  return {
    keys,
    value,
    simple: tr,
  };
}

async function trSimple(params: {
  keys: string[];
  value: string;
  langCode: string;
}): Promise<Translation> {
  // if (params.keys[0] !== "Billing") {
  //   return {
  //     tr: "",
  //     score: 0,
  //   };
  // }

  // generate context
  const { text: trContext } = await ai.generateText({
    model: registry.languageModel(trModel),
    temperature: 0,
    system: `You are a professional software engineer, specializing in explain the concept of keywords related to a website or App's user interface to a translator.`,
    prompt: `Your task is to carefully read some keywords that is related to your code, and explain these keywords in the context of a website's user interface. 
    The website you are working on is a SaaS.
    The keywords is delimited by XML tags <KEYWORDS></KEYWORDS>:
    <KEYWORDS>
    ${params.keys.join(",")}
    </KEYWORDS>

    When explain the keywords, please keep in mind that the transltor knows nothing about the website.
    Please explain the keywords using one sentense in the context of a SaaS product to the translator.
    Please start with "The text appears to be".
    Please use your own words and do not mentioned the keywords literally.
    `,
  });

  // translate
  const nrTranslations = 4;
  const { text: initialTr } = await ai.generateText({
    model: registry.languageModel(trModel),
    temperature: 0,
    system: `You are an expert linguist, specializing translate website's user interface.`,
    prompt: `Your task is to translate a source text to ${params.langCode} based on a translation context given by the website's developer. 
    The source text and translation context is delimited by XML tags <SOURCE_TEXT></SOURCE_TEXT> and <TRANSLATION_CONTEXT></TRANSLATION_CONTEXT> as below:
      <SOURCE_TEXT>
      ${params.value}
      </SOURCE_TEXT>

      <TRANSLATION_CONTEXT>
      ${trContext}
      </TRANSLATION_CONTEXT>


      Please follow these steps when doing the translation:
      Step 1: Infer the regional language corresponding to the language code ${params.langCode}.
      Step 2: Translate the source text into the corresponding regional language.

      When writing translation, pay attention to the translation context you received to make the translation more accurate.
      Do not provide any explanations or text apart from the translation.
      `,
  });

  //reflect
  const { text: trReflect } = await ai.generateText({
    model: registry.languageModel(trModel),
    temperature: 0.1,
    system: `You are an translator specializing in translation from en_US to ${params.langCode} in Website/App.
    You will be provided with a source text, a translation context related to the text, and a translation. Your goal is to improve the translation.`,
    prompt: `Your task is to carefully read a source text, a translation context, and a translation from en_US to ${params.langCode}, and then write ${nrTranslations} more translations with pros and cons.
    The final style and tone of the translation should match what is commonly used in a website's user interface in ${params.langCode}.
    
    The source text, translation context, initial translation, delimited by XML tags <SOURCE_TEXT></SOURCE_TEXT>, <TRANSLATION_CONTEXT></TRANSLATION_CONTEXT>, <TRANSLATION></TRANSLATION>, are as follows:
    <SOURCE_TEXT>
    ${params.value}
    </SOURCE_TEXT>

    <TRANSLATION_CONTEXT>
    ${trContext}
    </TRANSLATION_CONTEXT>

    <TRANSLATION>
    ${initialTr}
    </TRANSLATION>

    When writing pros/cons, pay attention that the following critirial of Pros (the opposit is Cons):
    (1) more fluency (by applying ${params.langCode} grammar, spelling and punctuation rules, and ensuring there are no unnecessary repetitions),
    (2) more prevalence (by ensuring the translation is mostly widely used in website in ${params.langCode})
    (3) more general (by ensuring the translation covers a broader range of contexts or meanings)

    Write a list of specific, helpful and constructive suggestions for improving the translation. Please write 3 new translation with speicic pros and cons.
Output only the suggestions releated to translation and nothing else.
    `,
  });

  // improve
  const { text: secondTr } = await ai.generateText({
    model: registry.languageModel(trModel),
    temperature: 0,
    system: `You are an expert linguist, specializing in translation editing from en_US to ${params.langCode} in a website's user interface.`,
    prompt: `Your task is to carefully read, then edit, a translation from en_US to ${params.langCode}, taking into
account a list of expert suggestions.

The source text, the initial translation, and the expert linguist suggestions are delimited by XML tags <SOURCE_TEXT></SOURCE_TEXT>, <TRANSLATION></TRANSLATION> and <EXPERT_SUGGESTIONS></EXPERT_SUGGESTIONS> \
as follows:

<SOURCE_TEXT>
${params.value}
</SOURCE_TEXT>

<TRANSLATION>
${initialTr}
</TRANSLATION>

<EXPERT_SUGGESTIONS>
${trReflect}
</EXPERT_SUGGESTIONS>

Please take into account the expert's new translation suggestions when editing the translation. Edit the translation by ensuring the translation is:

(1) more fluency (by applying ${params.langCode} grammar, spelling and punctuation rules, and ensuring there are no unnecessary repetitions),
(2) more prevalence (by ensuring the translation is mostly widely used in website in ${params.langCode})
(3) more general (by ensuring the translation covers a broader range of contexts or meanings)
(4) more concise (by ensuring the translation fit well in UI elements where brevity is prefereed)

Output only the new translation and nothing else.
`,
  });

  // review
  // const { object: trReview } = await ai.generateObject({
  //   model: registry.languageModel(trModel),
  //   schema: z.object({
  //     texts: z.array(
  //       z.object({
  //         text: z.string(),
  //         score: z.number(),
  //       })
  //     ),
  //   }),
  //   temperature: 0,
  //   system:
  //     `You are language text reviewer for a website. You are responsible for review a website's text. This website is a SaaS product.` +
  //     `You will receive the followings:
  //     1. A text context delimited by "###" that you are working on.
  //     2. Several text that requires review, each delimited by "---".
  // ` +
  //     `Please give each text that requires review a score.
  //   For each text that requires review, think carefully about if it is related to the context provided, and give a score from 1 to 10 to evaluate the text's quality. 10 is the best, 1 is the worst.
  //   For each text that requires review, ensure that the text that better describes the context gets a higher score.
  // `,
  //   prompt:
  //     `Text context: ###${trContext}###\n` +
  //     `${trObj.translations.map(
  //       (t, index) => `Text requires review ${index}: ---${t.translation}---.`
  //     )}`,
  // });

  // trReview.texts.sort((a, b) => b.score - a.score);

  console.log(`Key: ${params.keys}`);
  console.log(`Value: ${params.value}`);
  console.log(`Context: ${trContext}`);
  console.log(`initialTranslation: ${JSON.stringify(initialTr)}`);
  console.log(`reflections: ${JSON.stringify(trReflect)}`);
  console.log(`secondTranslation: ${secondTr}`);
  console.log(`---`);

  return {
    tr: "",
    score: 0,
  };
}

async function main() {
  const filePath = "en.json";
  const sourceObj = loadJSON(filePath);

  const promises = iterateObject(sourceObj);
  console.log("key count:", promises.length);
  const results = await Promise.all(promises);
  // print out results
  // for (let i = 0; i < results.length; i++) {
  //   const result = results[i];
  //   console.log(`Key: ${result.keys}`);
  //   console.log(`Value: ${result.value}`);
  //   console.log(`Simple Translation: ${result.simple.tr}`);
  //   console.log(`Score: ${result.simple.score}`);
  //   console.log("---"); // Separator for readability
  // }
}

main().catch(console.error);
