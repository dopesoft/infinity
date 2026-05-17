/**
 * Monaco themes - true-black + bone-white variants aligned with Studio's
 * tokens in globals.css. Applied via monaco.editor.defineTheme on first
 * load of any Monaco editor instance.
 *
 * Why custom themes: Monaco's built-in vs-dark has a #1e1e1e background
 * with a faint blue cast - collides with the OLED-friendly true-black
 * palette the rest of Studio uses. We mirror it as closely as possible
 * while keeping background = #000, surfaces a touch off-black, and the
 * accent colors desaturated.
 */

import type * as Monaco from "monaco-editor";

export const INFINITY_DARK = "infinity-dark";
export const INFINITY_LIGHT = "infinity-light";

export function registerInfinityThemes(monaco: typeof Monaco) {
  monaco.editor.defineTheme(INFINITY_DARK, {
    base: "vs-dark",
    inherit: true,
    rules: [
      { token: "", foreground: "f5f5f5", background: "000000" },
      { token: "comment", foreground: "6b7280", fontStyle: "italic" },
      { token: "keyword", foreground: "c084fc" },
      { token: "string", foreground: "86efac" },
      { token: "number", foreground: "fcd34d" },
      { token: "type", foreground: "67e8f9" },
      { token: "delimiter", foreground: "9ca3af" },
      { token: "variable", foreground: "f5f5f5" },
      { token: "function", foreground: "fda4af" },
    ],
    colors: {
      "editor.background": "#000000",
      "editor.foreground": "#f5f5f5",
      "editor.lineHighlightBackground": "#0d0d0d",
      "editor.lineHighlightBorder": "#0d0d0d",
      "editor.selectionBackground": "#1f1f1f",
      "editor.inactiveSelectionBackground": "#141414",
      "editorCursor.foreground": "#f5f5f5",
      "editorLineNumber.foreground": "#3f3f46",
      "editorLineNumber.activeForeground": "#a1a1aa",
      "editor.findMatchBackground": "#1f1f1f",
      "editor.findMatchHighlightBackground": "#141414",
      "editorGutter.background": "#000000",
      "editorGutter.modifiedBackground": "#facc15",
      "editorGutter.addedBackground": "#22c55e",
      "editorGutter.deletedBackground": "#ef4444",
      "editorIndentGuide.background": "#0a0a0a",
      "editorIndentGuide.activeBackground": "#1f1f1f",
      "editorWidget.background": "#0a0a0a",
      "editorWidget.border": "#1f1f1f",
      "editorSuggestWidget.background": "#0a0a0a",
      "editorSuggestWidget.border": "#1f1f1f",
      "editorSuggestWidget.selectedBackground": "#1f1f1f",
      "scrollbar.shadow": "#000000",
      "scrollbarSlider.background": "#1f1f1f",
      "scrollbarSlider.hoverBackground": "#2a2a2a",
      "scrollbarSlider.activeBackground": "#3f3f46",
      "diffEditor.insertedTextBackground": "#22c55e1f",
      "diffEditor.removedTextBackground": "#ef44441f",
      "diffEditor.insertedLineBackground": "#16a34a18",
      "diffEditor.removedLineBackground": "#dc262618",
      "diffEditorGutter.insertedLineBackground": "#16a34a40",
      "diffEditorGutter.removedLineBackground": "#dc262640",
      "diffEditorOverview.insertedForeground": "#22c55e",
      "diffEditorOverview.removedForeground": "#ef4444",
    },
  });

  monaco.editor.defineTheme(INFINITY_LIGHT, {
    base: "vs",
    inherit: true,
    rules: [
      { token: "comment", foreground: "6b7280", fontStyle: "italic" },
      { token: "keyword", foreground: "7c3aed" },
      { token: "string", foreground: "16a34a" },
      { token: "number", foreground: "b45309" },
      { token: "type", foreground: "0891b2" },
    ],
    colors: {
      "editor.background": "#ffffff",
      "editor.foreground": "#171717",
      "editor.lineHighlightBackground": "#fafafa",
      "editor.lineHighlightBorder": "#fafafa",
      "editorGutter.background": "#ffffff",
      "diffEditor.insertedLineBackground": "#22c55e14",
      "diffEditor.removedLineBackground": "#ef444414",
    },
  });
}
