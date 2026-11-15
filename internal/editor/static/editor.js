'use strict';
// decode переводит Base64URL в байты WebAuthn.
function decode(value) {
  const text = value.replace(/-/g, '+').replace(/_/g, '/');
  return Uint8Array.from(atob(text), char => char.charCodeAt(0));
}
// encode переводит байты WebAuthn в Base64URL.
function encode(value) {
  if (value === null || value === undefined) return null;
  return btoa(String.fromCharCode(...new Uint8Array(value)))
    .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
// post отправляет запрос аутентификации.
async function post(path, data) {
  const response = await fetch(path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(data)});
  if (!response.ok) throw new Error('Операция не выполнена. Начните заново.');
  return response.json();
}
const form = document.getElementById('auth-form');
if (form) form.addEventListener('submit', async event => {
  event.preventDefault();
  const status = document.getElementById('status');
  const button = form.querySelector('button');
  button.disabled = true;
  try {
    if (!window.PublicKeyCredential) throw new Error('Браузер не поддерживает Passkeys.');
    const kind = form.dataset.kind;
    const options = (await post(`/auth/${kind}/begin`, {Value: form.elements.value.value})).publicKey;
    form.elements.value.value = '';
    options.challenge = decode(options.challenge);
    if (options.user) options.user.id = decode(options.user.id);
    for (const key of ['allowCredentials', 'excludeCredentials']) {
      if (options[key]) options[key] = options[key].map(item => ({...item, id: decode(item.id)}));
    }
    const credential = kind === 'register'
      ? await navigator.credentials.create({publicKey: options})
      : await navigator.credentials.get({publicKey: options});
    const result = {id: credential.id, rawId: encode(credential.rawId), type: credential.type,
      authenticatorAttachment: credential.authenticatorAttachment,
      clientExtensionResults: credential.getClientExtensionResults(),
      response: {clientDataJSON: encode(credential.response.clientDataJSON)}};
    if (kind === 'register') {
      result.response.attestationObject = encode(credential.response.attestationObject);
      result.response.transports = credential.response.getTransports ? credential.response.getTransports() : [];
    } else {
      result.response.authenticatorData = encode(credential.response.authenticatorData);
      result.response.signature = encode(credential.response.signature);
      result.response.userHandle = encode(credential.response.userHandle);
    }
    await post(`/auth/${kind}/finish`, result);
    location.assign(kind === 'register' ? '/login' : '/');
  } catch (error) { status.textContent = error.message; }
  finally { button.disabled = false; }
});
