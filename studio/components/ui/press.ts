/**
 * Press feedback — what a control does the instant a finger lands on it.
 *
 * WHY THIS IS A SHARED CONSTANT AND NOT A CLASS EACH CONTROL PICKS
 *
 * The boss, on the nav: "it just doesn't feel as tactile as it should."
 * Every target had a `hover:` wash, which a touchscreen never fires, and the
 * one or two that had an `active:` state ran it through `transition-colors`
 * at the default 150ms — so a 90ms tap showed about half a colour and then
 * threw it away. The result was a nav you press and nothing at all happens
 * at the place you pressed, which is exactly what makes a person press again.
 *
 * `:active` is the ONLY feedback the browser can paint before the JavaScript
 * for the navigation runs, so it is the only thing that can be instant. The
 * transitions here are deliberately short for that reason: a press must be
 * fully arrived at within the length of a tap, not eased into.
 *
 * Two shapes, because there are two: a square icon target can take a squeeze,
 * a full-width row cannot without looking like it wobbled. Nothing else.
 * If you are about to write a third `active:` treatment, use one of these.
 */

/** Square icon targets: rail icons, header buttons, the app mark. */
export const PRESS_ICON =
  "transition-[background-color,color,opacity,transform] duration-100 active:scale-90 active:bg-accent";

/** Full-width rows: the nav drawer, list rows that navigate. */
export const PRESS_ROW =
  "transition-[background-color,color] duration-100 active:bg-accent";
