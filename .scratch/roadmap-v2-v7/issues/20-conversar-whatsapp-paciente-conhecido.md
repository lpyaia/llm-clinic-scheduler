# 20: V7 — Conversar pelo WhatsApp com paciente conhecido

**What to build:** Paciente já cadastrado envia uma mensagem pelo WhatsApp e recebe a resposta do agente no mesmo canal. O adapter resolve a identidade pelo número de origem validado e reutiliza a aplicação conversacional.

**Blocked by:** 19 — Processar mensagens concorrentes sem perder estado.

**Status:** ready-for-agent

- [ ] O webhook atende à verificação e valida a autenticidade das notificações conforme o provedor configurado; mensagens inválidas não chegam à aplicação.
- [ ] O número de origem do evento validado localiza o paciente e a conversa; identidade não é extraída de texto livre ou de parâmetros escolhidos pelo modelo.
- [ ] Uma mensagem de paciente conhecido percorre recepção, aplicação conversacional, ferramenta existente e envio de resposta em português.
- [ ] IDs de mensagem e identificadores de canal são transportados pelo adapter sem introduzir dependência de WhatsApp no domínio ou nas regras de agendamento.
- [ ] Eventos que não representam mensagem de paciente são reconhecidos sem invocar o agente; segredos do provedor são configurados fora do código e não aparecem nos logs.
- [ ] Testes com webhook e envio simulados verificam autenticidade, identidade e resposta; um roteiro de desenvolvimento documenta a demonstração com o provedor real.
