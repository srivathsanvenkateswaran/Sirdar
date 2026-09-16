/*
 * Language label on code blocks. pymdownx.highlight puts the fence's language
 * on the wrapper, <div class="language-yaml highlight">; CSS cannot read a
 * class name into a pseudo-element, so this copies it onto the wrapper as
 * data-lang and stylesheets/sirdar.css draws it. Plain and unlabelled fences
 * get nothing.
 */
(function () {
  var pattern = /(?:^|\s)language-([\w+#.-]+)/;
  function label() {
    var blocks = document.querySelectorAll(".md-typeset .highlight:not([data-lang])");
    for (var i = 0; i < blocks.length; i++) {
      var m = pattern.exec(blocks[i].className);
      if (!m) {
        var code = blocks[i].querySelector("code[class*='language-']");
        m = code && pattern.exec(code.className);
      }
      if (!m || m[1] === "text" || m[1] === "plain" || m[1] === "txt") continue;
      blocks[i].setAttribute("data-lang", m[1]);
    }
  }
  if (window.document$ && typeof window.document$.subscribe === "function") {
    window.document$.subscribe(label);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", label);
  } else {
    label();
  }
})();
