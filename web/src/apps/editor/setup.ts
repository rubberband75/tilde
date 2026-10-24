// CodeMirror 6 for the text editor (D48): the editing features xed has
// (line numbers, current line, bracket matching, search and replace,
// undo, indenting), syntax highlighting by file name, and colors from the
// theme's tokens. CodeMirror needs no eval and no workers, so the CSP's
// script-src stays 'self'; languages load as chunks of the build.
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import {
  bracketMatching,
  foldGutter,
  foldKeymap,
  HighlightStyle,
  indentOnInput,
  LanguageDescription,
  syntaxHighlighting,
} from '@codemirror/language'
import { languages } from '@codemirror/language-data'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { EditorState, type Extension } from '@codemirror/state'
import {
  crosshairCursor,
  drawSelection,
  dropCursor,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
  rectangularSelection,
} from '@codemirror/view'
import { tags as t } from '@lezer/highlight'

const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier, t.controlKeyword], color: 'var(--syntax-keyword)' },
  { tag: [t.string, t.special(t.string), t.regexp, t.character], color: 'var(--syntax-string)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--syntax-number)' },
  { tag: [t.comment, t.meta], color: 'var(--syntax-comment)', fontStyle: 'italic' },
  { tag: [t.typeName, t.className, t.namespace, t.tagName], color: 'var(--syntax-type)' },
  { tag: [t.function(t.variableName), t.function(t.propertyName), t.macroName], color: 'var(--syntax-function)' },
  { tag: [t.propertyName, t.attributeName, t.labelName], color: 'var(--syntax-property)' },
  { tag: t.heading, color: 'var(--syntax-heading)', fontWeight: 'bold' },
  { tag: [t.link, t.url], color: 'var(--syntax-link)', textDecoration: 'underline' },
  { tag: t.emphasis, fontStyle: 'italic' },
  { tag: t.strong, fontWeight: 'bold' },
  { tag: t.strikethrough, textDecoration: 'line-through' },
  { tag: t.invalid, color: 'var(--syntax-invalid)' },
])

const theme = EditorView.theme(
  {
    '&': { height: '100%', backgroundColor: 'var(--editor-bg)', color: 'var(--editor-fg)' },
    '.cm-scroller': { fontFamily: 'var(--font-mono)', fontSize: '14px', lineHeight: '1.45' },
    '.cm-content': { caretColor: 'var(--editor-cursor)' },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--editor-cursor)' },
    '.cm-gutters': {
      backgroundColor: 'var(--editor-gutter-bg)',
      color: 'var(--editor-gutter-fg)',
      border: 'none',
    },
    '.cm-activeLine': { backgroundColor: 'var(--editor-active-line-bg)' },
    '.cm-activeLineGutter': { backgroundColor: 'var(--editor-active-line-bg)', color: 'var(--editor-fg)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
      backgroundColor: 'var(--editor-selection-bg)',
    },
    '.cm-searchMatch': { backgroundColor: 'var(--editor-match-bg)' },
    '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'var(--editor-selection-bg)' },
    '.cm-selectionMatch': { backgroundColor: 'var(--hover-bg)' },
    '&.cm-focused .cm-matchingBracket': {
      backgroundColor: 'var(--hover-bg)',
      outline: '1px solid var(--editor-gutter-fg)',
    },
    '.cm-panels': { backgroundColor: 'var(--window-bg)', color: 'var(--window-fg)' },
    '.cm-panels.cm-panels-bottom': { borderTop: '1px solid var(--border)' },
    '.cm-panel input, .cm-panel button': { font: 'inherit' },
    '.cm-textfield': {
      backgroundColor: 'var(--entry-bg)',
      color: 'var(--entry-fg)',
      border: '1px solid var(--entry-border)',
      borderRadius: 'var(--radius)',
    },
    '.cm-button': {
      backgroundImage: 'none',
      backgroundColor: 'var(--button-bg)',
      color: 'var(--button-fg)',
      border: '1px solid var(--button-border)',
      borderRadius: 'var(--radius)',
    },
    '.cm-foldPlaceholder': { backgroundColor: 'var(--button-bg)', color: 'var(--window-fg)', border: 'none' },
    '.cm-tooltip': { backgroundColor: 'var(--menu-bg)', color: 'var(--menu-fg)', border: 'none' },
  },
  { dark: true },
)

/** Everything but the language, word wrap and the editor's own keys. */
export function baseExtensions(): Extension[] {
  return [
    lineNumbers(),
    highlightActiveLineGutter(),
    foldGutter(),
    highlightSpecialChars(),
    history(),
    drawSelection(),
    dropCursor(),
    EditorState.allowMultipleSelections.of(true),
    indentOnInput(),
    syntaxHighlighting(highlight),
    bracketMatching(),
    rectangularSelection(),
    crosshairCursor(),
    highlightActiveLine(),
    highlightSelectionMatches(),
    keymap.of([...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, indentWithTab]),
    theme,
  ]
}

/** The language for a file name, by extension or name ("Makefile"). */
export function languageFor(name: string): LanguageDescription | null {
  return LanguageDescription.matchFilename(languages, name)
}
