function validateField(input) {
  const err = input.parentElement.querySelector('.field-error');
  if (err) err.remove();
  input.classList.remove('input-error');
  if (input.type === 'number' && input.value !== '' && isNaN(Number(input.value))) {
    input.classList.add('input-error');
    const msg = document.createElement('span');
    msg.className = 'field-error text-xs text-destructive mt-0.5';
    msg.textContent = 'Enter a valid number';
    input.parentElement.appendChild(msg);
    return false;
  }
  return true;
}

function clearFieldError(input) {
  const err = input.parentElement.querySelector('.field-error');
  if (err) { err.remove(); input.classList.remove('input-error'); }
}

function validateForm(form) {
  let valid = true;
  form.querySelectorAll('input[type="number"]').forEach(function(inp) {
    if (!validateField(inp)) valid = false;
  });
  return valid;
}
