# Demonstrar WhatsApp com paciente cadastrado

Use o número comercial de teste do WhatsApp Cloud API e um telefone de teste sob seu controle. Adicione esse telefone
como destinatário no painel da Meta. Use somente dados fictícios no banco local.

## Configurar a aplicação

Siga a [configuração local](../README.md#executar-localmente) para iniciar o PostgreSQL, aplicar as migrations e criar
os horários de teste. Preencha `OPENAI_API_KEY` e estas variáveis na `.env` da raiz:

| Variável | Valor |
| --- | --- |
| `WHATSAPP_PROVIDER` | `meta` |
| `WHATSAPP_META_PHONE_NUMBER_ID` | **Phone number ID** do número comercial de teste no painel da Meta |
| `WHATSAPP_META_VERIFY_TOKEN` | Segredo aleatório escolhido por você para verificar a URL do webhook |
| `WHATSAPP_META_APP_SECRET` | **App secret** nas configurações do app da Meta |
| `WHATSAPP_META_ACCESS_TOKEN` | Token de teste com permissão `whatsapp_business_messaging` |

Reinicie `go run ./cmd/clinic serve` depois de alterar o ambiente. Use a sessão de desenvolvimento local para
cadastrar o telefone de teste com o mesmo número de origem em E.164:

```powershell
$phone = Read-Host 'Telefone de teste em E.164, incluindo +'
$body = @{ full_name = 'Paciente de teste'; phone = $phone } | ConvertTo-Json
$session = Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:3000/v1/dev/sessions' `
    -ContentType 'application/json' -Body $body
```

Para demonstrar uma lista preenchida, use o token de `$session` para criar um atendimento fictício pelo
[fluxo local de agendamento](../README.md#fluxo-completo-via-http).

## Conectar à Meta e testar

1. Use um proxy ou túnel com HTTPS público e certificado válido. Encaminhe apenas `GET` e `POST` de
   `/webhooks/whatsapp` para `http://127.0.0.1:3000/webhooks/whatsapp`. Mantenha `/v1/dev/sessions` somente local.
   Desative a captura de query strings, corpos e headers no proxy ou túnel, pois a verificação leva um token na URL.
2. No painel do app da Meta, use essa URL pública como **Callback URL** e o valor de `WHATSAPP_META_VERIFY_TOKEN`
   como **Verify token**. Salve, assine o campo `messages` e confirme a assinatura do app na conta de teste.
3. No WhatsApp do telefone cadastrado, envie **Quais são meus agendamentos?** ao número comercial de teste.
   O agente deve responder no mesmo chat, em português, com os atendimentos do paciente ou a lista vazia.
4. Envie **Pode repetir?** para testar a continuidade da conversa. A abertura ou entrega da resposta também gera
   um evento de status, que recebe HTTP 200 sem passar pelo agente.

Consulte a [configuração oficial do webhook da Meta][webhook] e a [referência de mensagens][messages].

## Comportamento do canal

- A Meta assina o corpo bruto do webhook. Uma assinatura inválida recebe HTTP 403. A busca usa o `from` assinado
  da mensagem, normalizado em E.164, e o `phone_number_id` configurado delimita o canal comercial.
- O canal aceita mensagens de texto até 4000 bytes por turno e notificações da Meta de até 3 MiB.
  Números desconhecidos, eventos sem o telefone de origem, mídias e notificações de status são reconhecidos com
  HTTP 200. O cadastro pelo WhatsApp é tratado na [issue #29][onboarding].
- O aplicativo persiste a resposta ou orientação segura de falha antes de chamar a Graph API. O webhook tenta as
  demais mensagens do lote mesmo se um envio falhar, e retorna HTTP 503 ao final se houve falha recuperável.
  Uma reentrega reutiliza os resultados salvos e pode repetir envios. O estado durável de entrega e as
  retentativas controladas são tratados na [issue #30][delivery].
- Consultas e busca de horários funcionam por mensagem de texto. Para concluir uma operação com proposta, o canal
  orienta o paciente a falar com a equipe da clínica. A confirmação exige um vínculo com a revisão do resumo entregue.
- Os logs da aplicação registram rota, status e duração. Use `CLINIC_TRACE_SYNTHETIC=true` somente com dados fictícios.

## Teste automatizado

```powershell
go test ./internal/adapters/whatsapp
./scripts/test-integration.ps1 -run TestWhatsAppKnownPatientReceivesOwnAppointments
```

O teste de integração usa PostgreSQL e a ferramenta real de consulta de agendamentos. O modelo e a Graph API são
simulados. O teste verifica a assinatura, a resolução do paciente, a continuidade e a resposta no canal correto.

[webhook]: https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/create-webhook-endpoint
[messages]: https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/reference/messages
[onboarding]: https://github.com/lpyaia/llm-clinic-scheduler/issues/29
[delivery]: https://github.com/lpyaia/llm-clinic-scheduler/issues/30
