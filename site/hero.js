/*
 * Sirdar landing page — the ring, the marquee, and the copy control.
 *
 * The page is readable and complete without this file: the sources row is a
 * wrapped list and the install command can be selected by hand. All this
 * script does is draw the ring, set the sources row moving, and save a
 * selection. It respects prefers-reduced-motion by leaving the row still.
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

  /* The marquee ---------------------------------------------------------
     The track is duplicated once, with the copies hidden from assistive
     technology, and translated by half its width, so the loop has no seam.
     Under reduced motion the row stays a wrapped list and nothing is cloned. */

  function buildMarquee() {
    var wrap = document.getElementById("marquee");
    var track = document.getElementById("marquee-track");
    if (!wrap || !track || reduce) { return; }

    var items = Array.prototype.slice.call(track.children);
    items.forEach(function (item) {
      var copy = item.cloneNode(true);
      copy.setAttribute("aria-hidden", "true");
      copy.setAttribute("data-clone", "");
      track.appendChild(copy);
    });
    wrap.classList.add("on");
    track.classList.add("on");
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
  buildMarquee();
  wireCopy();
})();
