import { EditorState, Compartment } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, dropCursor, highlightSpecialChars } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { bracketMatching, foldGutter, indentOnInput, syntaxHighlighting, defaultHighlightStyle, StreamLanguage } from "@codemirror/language";
import { searchKeymap, highlightSelectionMatches } from "@codemirror/search";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { markdown } from "@codemirror/lang-markdown";
import { html } from "@codemirror/lang-html";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { protobuf } from "@codemirror/legacy-modes/mode/protobuf";
import { MAX_SOURCE_BYTES } from "../assets/core.mjs";

const encoder = new TextEncoder();
const languages = { markdown, html, json, yaml, text: () => [], protobuf: () => StreamLanguage.define(protobuf) };

export function createEditor(parent, { content, language = "markdown", editable = true, nonce = "", dark = false, onChange = () => {}, onError = () => {} }) {
  const permission = new Compartment();
  const appearance = new Compartment();
  let current = content;
  let bytes;
  const theme = value => EditorView.theme({ "&": { height: "100%" }, ".cm-focused": { outline: "none" }, ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { background: value ? "#31578b" : "#dbe7ff" } }, { dark: value });
  const byteChange = transaction => {
    let delta = 0;
    transaction.changes.iterChanges((from, to, _a, _b, inserted) => {
      delta -= encoder.encode(transaction.startState.sliceDoc(from, to)).length;
      delta += encoder.encode(inserted.sliceString(0, inserted.length, transaction.startState.lineBreak)).length;
    });
    return delta;
  };
  const lineBreak = content.includes("\r\n") && !content.replaceAll("\r\n", "").includes("\n") ? "\r\n" : "\n";
  const state = EditorState.create({ doc: content, extensions: [
    EditorState.lineSeparator.of(lineBreak), lineNumbers(), highlightActiveLineGutter(), highlightSpecialChars(), history(), drawSelection(), dropCursor(), foldGutter(), indentOnInput(), bracketMatching(), closeBrackets(), highlightActiveLine(), highlightSelectionMatches(), syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
    keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap]),
    (languages[language] || languages.text)(), permission.of([EditorState.readOnly.of(!editable), EditorView.editable.of(editable)]), appearance.of(theme(dark)), EditorView.cspNonce.of(nonce), EditorView.contentAttributes.of({ "aria-label": "Document source. Press Escape then Tab to leave the editor." }),
    EditorState.transactionFilter.of(transaction => {
      if (!transaction.docChanged || bytes + byteChange(transaction) <= MAX_SOURCE_BYTES) return transaction;
      onError(new Error("That edit would exceed the 4 MiB source limit. Your previous draft is unchanged."));
      return [];
    }),
    EditorView.updateListener.of(update => {
      if (!update.docChanged) return;
      for (const transaction of update.transactions) bytes += byteChange(transaction);
      current = update.state.sliceDoc(); onChange(current);
    }),
  ] });
  bytes = encoder.encode(state.sliceDoc()).length;
  const view = new EditorView({ state, parent });
  return {
    value: () => current,
    refresh: () => view.requestMeasure(),
    focus: () => view.focus(),
    setEditable: value => view.dispatch({ effects: permission.reconfigure([EditorState.readOnly.of(!value), EditorView.editable.of(value)]) }),
    setDark: value => view.dispatch({ effects: appearance.reconfigure(theme(value)) }),
    replace(before, after = "") {
      const { from, to } = view.state.selection.main;
      const selected = view.state.sliceDoc(from, to);
      view.dispatch({ changes: { from, to, insert: before + selected + after }, selection: { anchor: from + before.length, head: from + before.length + selected.length } });
      view.focus();
    },
    destroy: () => view.destroy(),
  };
}
