/*
 * Sirdar landing page — the ring, the hero sheet, and the copy control.
 *
 * The page is readable and complete without this file: the sheet renders as
 * the finished note and the install command can be selected by hand. All this
 * script does is rewind the sheet and play it once, draw the ring, and save a
 * selection. It respects prefers-reduced-motion by jumping to the end state.
 */
(function () {
  "use strict";

  var reduce = window.matchMedia
    && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* The ring ------------------------------------------------------------
     The sentence is the product's own scope, so the decoration is also
     information. Characters are laid on the circle at an even angular step;
     the repeat count is chosen so the glyphs sit about one advance apart. */

  function buildRing() {
    var ring = document.getElementById("ring");
    if (!ring) { return; }

    var phrase = "triage / evidence / root cause / proposed fix / rca / resolution / ";
    var cs = window.getComputedStyle(ring);
    var radius = parseFloat(cs.getPropertyValue("--ring-r")) || 190;
    var advance = (parseFloat(cs.fontSize) || 13) * 0.62;
    var reps = Math.max(1, Math.round((2 * Math.PI * radius) / advance / phrase.length));
    var text = new Array(reps + 1).join(phrase);
    var step = 360 / text.length;
    var frag = document.createDocumentFragment();

    for (var i = 0; i < text.length; i++) {
      var ch = text.charAt(i);
      if (ch === " ") { continue; }
      var span = document.createElement("span");
      span.textContent = ch;
      span.style.transform = "translate(-50%, -50%) rotate("
        + (i * step).toFixed(3) + "deg) translateY(-" + radius + "px)";
      frag.appendChild(span);
    }
    ring.appendChild(frag);
  }

  /* The sheet -----------------------------------------------------------
     A rule travels down the ticket once. As it passes a row, that row stops
     being the customer's Arabic and becomes the note's English field. One
     orchestrated moment; nothing else on this page moves on its own. */

  function playSheet() {
    var sheet = document.getElementById("sheet");
    if (!sheet) { return; }

    var rows = Array.prototype.slice.call(sheet.querySelectorAll(".row"));
    var scan = document.getElementById("scan");
    var chip = document.getElementById("sheet-state");
    var foot = document.getElementById("sheet-foot");
    if (!rows.length) { return; }

    function finish() {
      for (var i = 0; i < rows.length; i++) { rows[i].classList.add("done"); }
      if (chip) {
        chip.setAttribute("data-state", "triaged");
        chip.textContent = "triaged";
      }
      if (foot) { foot.classList.remove("pending"); }
      if (scan) { scan.classList.remove("on"); }
    }

    if (reduce) { finish(); return; }

    var first = rows[0];
    var last = rows[rows.length - 1];
    var from = first.offsetTop;
    var to = last.offsetTop + last.offsetHeight;
    var duration = 2600;

    // When the rule reaches a row, that row turns over. The times are worked
    // out up front from the inverse of the easing, so the sequence is driven
    // by timers and a CSS transition rather than a frame loop: a browser that
    // throttles animation frames still lands on the finished note.
    var schedule = rows.map(function (row) {
      var mid = row.offsetTop + row.offsetHeight * 0.45;
      var p = Math.max(0, Math.min(1, (mid - from) / (to - from)));
      return (1 - Math.pow(1 - p, 1 / 3)) * duration;
    });

    function start() {
      scan.classList.add("on");
      scan.style.transform = "translateY(" + from + "px)";
      void scan.offsetHeight; // flush, so the transition has a start value
      scan.style.transition = "transform " + duration + "ms var(--sd-ease)";
      scan.style.transform = "translateY(" + to + "px)";

      rows.forEach(function (row, i) {
        window.setTimeout(function () { row.classList.add("done"); }, schedule[i]);
      });
      window.setTimeout(finish, duration + 80);
    }

    // Measure after the display serif and the Naskh face land, but never wait
    // on a font promise that does not settle.
    var started = false;
    function once() { if (!started) { started = true; window.setTimeout(start, 250); } }
    if (document.fonts && document.fonts.ready) { document.fonts.ready.then(once, once); }
    window.setTimeout(once, 1200);
  }

  /* The copy control ----------------------------------------------------- */

  function wireCopy() {
    var buttons = document.querySelectorAll("[data-copy]");
    var status = document.getElementById("copystatus");

    Array.prototype.forEach.call(buttons, function (btn) {
      var label = btn.querySelector("[data-copy-label]") || btn;
      var target = document.querySelector(btn.getAttribute("data-copy"));
      if (!target) { return; }

      function say(text, announcement) {
        label.textContent = text;
        if (status) { status.textContent = announcement || ""; }
        window.setTimeout(function () {
          label.textContent = "Copy";
          if (status) { status.textContent = ""; }
        }, 2400);
      }

      function selectIt() {
        var range = document.createRange();
        range.selectNodeContents(target);
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        try {
          document.execCommand("copy");
          say("Copied", "Install command copied");
        } catch (err) {
          say("Selected", "Install command selected; press the copy shortcut");
        }
      }

      btn.addEventListener("click", function () {
        var text = target.textContent.trim();
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () {
            say("Copied", "Install command copied");
          }, selectIt);
        } else {
          selectIt();
        }
      });
    });
  }

  buildRing();
  playSheet();
  wireCopy();
})();
