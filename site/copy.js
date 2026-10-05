const prompt = document.getElementById("setup-prompt");
const button = document.getElementById("copy-prompt");
const status = document.getElementById("copy-status");

button.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(prompt.textContent);
    status.textContent = "Setup prompt copied.";
  } catch {
    prompt.focus();
    const range = document.createRange();
    range.selectNodeContents(prompt);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    status.textContent = "Copy the selected prompt with your browser's Copy command, then paste it into your agent.";
  }
});
button.hidden = false;
