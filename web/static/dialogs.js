/**
 * notty/dialogs.js
 * ─────────────────────────────────────────────────────────────────────────────
 * Manages:
 *   1. Generic <dialog> open / close via data-* attributes.
 *   2. HTMX form success → close dialog + return focus to trigger.
 *   3. Command Palette (Ctrl+K / Cmd+K) with ARIA combobox navigation.
 *
 * No external dependencies.  Works alongside HTMX 1.x.
 * ─────────────────────────────────────────────────────────────────────────────
 */

/* Expose a namespace so inline hx-on handlers can call into this module. */
window.notty = window.notty || {};
window.notty.dialogs = (function () {
  'use strict';

  // ── Helpers ──────────────────────────────────────────────────────────────

  /** Return the <dialog> element with the given id, or null. */
  function getDialog(id) {
    return document.getElementById(id);
  }

  /**
   * Open a dialog and remember which element triggered it so focus can be
   * returned when the dialog closes.
   *
   * @param {string}      dialogId  - id of the <dialog> element
   * @param {HTMLElement} [trigger] - the button that caused the open
   */
  function openDialog(dialogId, trigger) {
    var dlg = getDialog(dialogId);
    if (!dlg) return;

    // Store the trigger so closeDialog() can return focus.
    if (trigger) dlg._trigger = trigger;

    dlg.showModal();

    // Auto-focus the first interactive field inside the dialog body.
    var first = dlg.querySelector(
      '.dialog-body input, .dialog-body textarea, .dialog-body select'
    );
    if (first) {
      // Slight delay lets the browser finish the showModal paint.
      requestAnimationFrame(function () { first.focus(); });
    }
  }

  /**
   * Close a dialog, reset its form(s), and return focus to the trigger.
   *
   * @param {string} dialogId
   */
  function closeDialog(dialogId) {
    var dlg = getDialog(dialogId);
    if (!dlg) return;

    dlg.close();

    // Clear all inline validation messages and reset fields.
    dlg.querySelectorAll('form').forEach(function (f) { f.reset(); });
    dlg.querySelectorAll('.form-error').forEach(function (el) {
      el.textContent = '';
    });

    // Return focus to whatever opened the dialog.
    if (dlg._trigger && typeof dlg._trigger.focus === 'function') {
      dlg._trigger.focus();
      dlg._trigger = null;
    }
  }

  // ── Generic open / close wiring (data attributes) ────────────────────────
  //
  //   Open:  <button data-open-dialog="dlg-id">
  //   Close: <button data-close-dialog="dlg-id">
  //
  document.addEventListener('click', function (ev) {
    var btn = ev.target.closest('[data-open-dialog]');
    if (btn) {
      openDialog(btn.dataset.openDialog, btn);
      return;
    }

    btn = ev.target.closest('[data-close-dialog]');
    if (btn) {
      closeDialog(btn.dataset.closeDialog);
    }
  });

  // Close on backdrop click (clicking outside the dialog box itself).
  document.addEventListener('click', function (ev) {
    if (ev.target.tagName !== 'DIALOG') return;
    // ev.target IS the <dialog>; a click on the box interior bubbles to
    // the dialog but ev.target is then the inner element.
    var rect = ev.target.getBoundingClientRect();
    var outside =
      ev.clientX < rect.left  || ev.clientX > rect.right  ||
      ev.clientY < rect.top   || ev.clientY > rect.bottom;
    if (outside) {
      // Find dialog id to use closeDialog() (so form is reset + focus returned).
      closeDialog(ev.target.id);
    }
  });

  // ── HTMX form-success callback ────────────────────────────────────────────
  /**
   * Called from hx-on--after-request on each dialog form element.
   * Closes the dialog only when the HTTP response is a success (2xx).
   *
   * @param {string}      dialogId
   * @param {string}      triggerId  - id of the sidebar button (for focus return)
   * @param {HTMLElement} formEl
   */
  function onFormSuccess(dialogId, triggerId, formEl) {
    // HTMX sets htmx-request on the element during the request.
    // After the request the element carries the xhr on its detail; we can
    // read it from the last HTMX event stored on the form.
    var xhr = formEl && formEl._htmxXhr;
    if (xhr && xhr.status >= 400) return; // server-side validation error — keep dialog open

    // Ensure the trigger element is set for focus-return before closing.
    var dlg = getDialog(dialogId);
    if (dlg && !dlg._trigger) {
      var t = document.getElementById(triggerId);
      if (t) dlg._trigger = t;
    }
    closeDialog(dialogId);
  }

  // Capture the XHR on the form element so onFormSuccess can inspect it.
  document.addEventListener('htmx:beforeRequest', function (ev) {
    if (ev.detail && ev.detail.elt) {
      ev.detail.elt._htmxXhr = null; // reset
    }
  });
  document.addEventListener('htmx:afterRequest', function (ev) {
    if (ev.detail && ev.detail.elt && ev.detail.xhr) {
      ev.detail.elt._htmxXhr = ev.detail.xhr;
    }
  });

  // ── Command Palette ───────────────────────────────────────────────────────

  var cmdPalette  = null; // lazily resolved
  var cmdInput    = null;
  var cmdResults  = null;
  var cmdAnnounce = null;

  function initCmdRefs() {
    if (cmdPalette) return;
    cmdPalette  = document.getElementById('cmd-palette');
    cmdInput    = document.getElementById('cmd-input');
    cmdResults  = document.getElementById('cmd-results');
    cmdAnnounce = document.getElementById('cmd-sr-announce');
  }

  /** Open the command palette dialog. */
  function openCmdPalette() {
    initCmdRefs();
    if (!cmdPalette) return;

    cmdPalette.showModal();
    if (cmdInput) {
      cmdInput.value = '';
      cmdInput.setAttribute('aria-expanded', 'false');
      cmdInput.setAttribute('aria-activedescendant', '');
      requestAnimationFrame(function () { cmdInput.focus(); });
    }
    if (cmdResults) cmdResults.innerHTML = '';
    if (cmdAnnounce) cmdAnnounce.textContent = '';
  }

  /** Close the command palette. */
  function closeCmdPalette() {
    initCmdRefs();
    if (!cmdPalette) return;
    cmdPalette.close();
  }

  // Ctrl+K / Cmd+K — open the palette.
  document.addEventListener('keydown', function (ev) {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'k') {
      ev.preventDefault();
      openCmdPalette();
    }
  });

  // ── ARIA combobox keyboard navigation ────────────────────────────────────
  //
  //   Arrow Down / Arrow Up  → move the active descendant through the listbox.
  //   Enter                  → follow the active result link.
  //   Escape                 → native <dialog> already handles close.
  //
  //   Physical focus STAYS on the <input> at all times so the user can
  //   keep typing.  Visual selection is conveyed via aria-selected and
  //   the .cmd-result[aria-selected="true"] CSS rule.
  //

  document.addEventListener('keydown', function (ev) {
    initCmdRefs();
    if (!cmdInput || document.activeElement !== cmdInput) return;

    var items = cmdResults
      ? Array.from(cmdResults.querySelectorAll('[role="option"]'))
      : [];

    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault(); // prevent page scroll
      if (!items.length) return;

      var current = cmdResults.querySelector('[aria-selected="true"]');
      var idx = current ? items.indexOf(current) : -1;

      if (ev.key === 'ArrowDown') {
        idx = (idx + 1) % items.length;
      } else {
        idx = (idx - 1 + items.length) % items.length;
      }

      // Clear old selection.
      if (current) current.setAttribute('aria-selected', 'false');

      var next = items[idx];
      next.setAttribute('aria-selected', 'true');
      cmdInput.setAttribute('aria-activedescendant', next.id);
      cmdInput.setAttribute('aria-expanded', 'true');

      // Scroll the option into view without moving visual focus.
      next.scrollIntoView({ block: 'nearest' });
      return;
    }

    if (ev.key === 'Enter') {
      var active = cmdResults
        ? cmdResults.querySelector('[aria-selected="true"]')
        : null;
      if (active) {
        ev.preventDefault();
        var link = active.tagName === 'A' ? active : active.querySelector('a');
        if (link) link.click();
        closeCmdPalette();
      }
    }
  });

  /**
   * Called by hx-on--after-request on #cmd-input after HTMX fetches results.
   * Updates the combobox aria-expanded state and announces the result count.
   */
  function onCmdResults() {
    initCmdRefs();
    if (!cmdResults || !cmdInput) return;

    var items = cmdResults.querySelectorAll('[role="option"]');
    var count = items.length;

    // Reset selection state.
    items.forEach(function (el) { el.setAttribute('aria-selected', 'false'); });
    cmdInput.setAttribute('aria-expanded', count > 0 ? 'true' : 'false');
    cmdInput.setAttribute('aria-activedescendant', '');

    // Announce to screen readers.
    if (cmdAnnounce) {
      cmdAnnounce.textContent =
        count === 0
          ? (cmdResults.dataset.emptyMsg || 'No results')
          : count + ' result' + (count === 1 ? '' : 's');
    }
  }

  // ── Public API ────────────────────────────────────────────────────────────
  return {
    openDialog:     openDialog,
    closeDialog:    closeDialog,
    onFormSuccess:  onFormSuccess,
    openCmdPalette: openCmdPalette,
    onCmdResults:   onCmdResults,
  };
}());
