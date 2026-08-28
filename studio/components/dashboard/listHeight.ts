/* Dashboard list sizing — ONE constant, so cards in a row line up.
 *
 * Why this exists (bug the boss reported 2026-08-28: "the row where todos is,
 * seems a bit above calendar and pursuits, it's not at the same level"):
 *
 * The dashboard used to align its cards by MEASUREMENT. `FollowUpsCard` in row
 * one measured its rendered list via `ScrollList.onMeasure`, handed the pixel
 * height up to `DashboardClient`, and that number was threaded back down as
 * `matchHeight` into some — but not all — of the other cards. Two failures fell
 * out of that:
 *
 *   1. Cards in row TWO were pinned to a height measured in row ONE, and
 *      `PursuitsCard` was never given the prop at all, so one card in the row
 *      sized itself while its neighbours were clamped to a borrowed number.
 *   2. The measurement lands one paint AFTER first render, so the row visibly
 *      settles: cards start at their natural heights and jump.
 *
 * The fix is to align by LAYOUT, not by measurement. Every list card clips at
 * the same number of rows, the CSS grid stretches the cells, and the row is
 * aligned on the first paint with no measuring, no prop threading, and no
 * cross-row coupling. A card that wants a different density passes its own
 * `max` — but the default is shared, so "they should match" is expressed once
 * here instead of being re-derived in five components.
 *
 * `matchHeight` props are still accepted by the cards for now so nothing
 * breaks; they are simply no longer threaded by the dashboard.
 */

/** Rows a dashboard list card shows before it scrolls internally. */
export const DASHBOARD_LIST_ROWS = 4;
