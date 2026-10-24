# 21: V7 — Cadastrar paciente no primeiro contato

**What to build:** Quando um número ainda desconhecido inicia contato pelo WhatsApp, a conversa coleta o nome completo e cadastra ou vincula o paciente antes de permitir acesso a agendamentos.

**Blocked by:** 20 — Conversar pelo WhatsApp com paciente conhecido.

**Status:** ready-for-agent

- [ ] Um número desconhecido inicia onboarding com estado persistido e solicita nome completo sem exigir senha ou introduzir OTP nesta entrega.
- [ ] O telefone vem exclusivamente da origem validada pelo adapter, é normalizado em E.164 e não pode ser substituído por um telefone declarado na mensagem.
- [ ] Após fornecer um nome válido, o paciente é criado ou localizado pela identidade existente e segue para a conversa de agendamento.
- [ ] Mensagens repetidas ou concorrentes no onboarding não criam pacientes duplicados nem vinculam a conversa a outra pessoa.
- [ ] Antes da identificação, a conversa não lista nem altera agendamentos; o backend resolve o principal e preserva a possibilidade futura de identity_verified.
- [ ] Testes verificam número novo, nome inválido, retomada do onboarding após reinício, paciente já existente e cadastro concorrente.
