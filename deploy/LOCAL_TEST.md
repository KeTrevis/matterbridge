# Tests locaux Slack ↔ Discord

Le bridge local relie uniquement ces deux salons :

- Slack : https://teamgaladrim.slack.com/archives/C0CCT9995FZ
- Discord : https://discord.com/channels/1422237626366431270/1565380845555023893

Il utilise `matterbridge.test.toml`. En mode natif, sa base reste dans
`data/test/`. En mode Docker, il utilise l'image `slack-discord-bridge:test`
et le volume SQLite `slack-discord-bridge-test_bridge-data`.

Les images et vidéos sont téléchargées puis réuploadées dans les deux sens,
avec une limite de 10 Mo par fichier (`MediaDownloadSize = 10000000`).

## Application Slack de test

Créer une application Slack distincte est nécessaire : les connexions Socket
Mode d'une même application se partagent les événements, même si les bridges
configurent des salons différents ([documentation Slack](https://docs.slack.dev/apis/events-api/using-socket-mode/#using-multiple-connections)).

1. Sur https://api.slack.com/apps, choisir **Create New App → From an app manifest**.
2. Choisir le workspace Galadrim et coller `deploy/slack-test-manifest.json`.
3. Installer l'application dans le workspace, puis récupérer son **Bot User OAuth Token**
   (`xoxb-…`) dans **OAuth & Permissions**.
4. Dans **Basic Information → App-Level Tokens**, générer un token (`xapp-…`)
   avec le scope **connections:write**. Le manifeste active déjà Socket Mode.
5. Dans le salon Slack de test, inviter **matterbridge-test**.

Mettre ces deux tokens dans `deploy/.env.test`, aux clés `TEST_SLACK_BOT_TOKEN`
et `TEST_SLACK_APP_TOKEN`. Ce fichier est ignoré par Git. Le token Discord
existant y est déjà renseigné sous `TEST_DISCORD_BOT_TOKEN` sur cette machine.
Sur une nouvelle machine, copier `deploy/test.env.example` vers `deploy/.env.test`
et renseigner les trois valeurs. Ne pas utiliser les tokens Slack du serveur.

## Lancement natif Go

Depuis la racine du dépôt, avec Go installé :

```sh
sh scripts/test-local.sh
```

Le script compile le bridge et le lance au premier plan. Arrêter avec `Ctrl+C`.
La base SQLite et le binaire restent dans `data/test/`, ignoré par Git.
Relancer le script pour vérifier que les threads restent associés après redémarrage.

## Lancement Docker

Alternative avec Docker, depuis la racine du dépôt :

```sh
docker compose --env-file deploy/.env.test -f deploy/compose.test.yaml up -d --build
docker compose --env-file deploy/.env.test -f deploy/compose.test.yaml logs -f bridge
```

Arrêter le bridge local :

```sh
docker compose --env-file deploy/.env.test -f deploy/compose.test.yaml stop
```

Vérifier un nouveau message puis une réponse dans son thread, dans les deux sens.
Redémarrer ensuite le bridge local et répondre dans le même thread pour vérifier
la persistance SQLite :

```sh
docker compose --env-file deploy/.env.test -f deploy/compose.test.yaml restart bridge
```

La base conserve les correspondances créées à partir de cette version. Les
associations déjà perdues par l'ancien bridge ne sont pas récupérables automatiquement.
Ne pas ajouter `-v` à `down` pour conserver les associations.

## Nom et avatar des auteurs

Les messages texte affichent le nom et l’avatar de leur auteur, avec le suffixe
`[Slack]` ou `[Discord]`, y compris dans les threads. Le nom n’est plus ajouté
au début du texte. Cela concerne les nouveaux messages, sans renommer les anciens.

Le bot Discord doit avoir **Gérer les webhooks** dans le salon relié, ainsi que
les permissions de création et d’écriture dans les threads publics. La suppression
des messages de webhook via le bot nécessite **Gérer les messages**.
L’application Slack doit avoir le scope **chat:write.customize** ; si ce scope
vient d’être ajouté, réinstaller l’application dans le workspace pour l’accorder.
Le manifeste fourni contient déjà ce scope.

Les fichiers envoyés vers Slack utilisent toujours l’identité de l’application,
avec le nom de l’auteur dans le commentaire du fichier.

## Réactions Slack ↔ Discord

Les ajouts et retraits de réactions standard sont relayés dans les deux sens,
y compris dans les threads. Chaque emoji apparaît sous l'identité du bot sur
l'autre plateforme : il reste présent tant qu'au moins une personne l'utilise
à la source. Les compteurs et les noms des personnes ne sont pas reproduits.
Les réactions du bot sont ignorées pour éviter les boucles.

Dans l'application Slack, ajouter le scope **reactions:write** (en plus de
**reactions:read**) et les événements **reaction_added** et **reaction_removed**,
puis réinstaller l'application dans le workspace. Les deux manifestes fournis
incluent ces paramètres. Le bot Discord doit avoir **Ajouter des réactions** et
**Voir les anciens messages** dans les salons reliés.

Seuls les messages dont le bridge possède encore la correspondance peuvent
recevoir une réaction synchronisée ; SQLite conserve ces correspondances après
redémarrage. Les réactions déjà présentes avant le lancement ne sont pas reprises
automatiquement. Les emojis personnalisés, les Super Réactions Discord et les
suppressions groupées par modération ne sont pas synchronisés. Slack doit utiliser
la connexion Socket Mode avec token bot.

Pour vérifier les réactions, envoyer un message dans chaque sens, puis réagir
sur le message original et sur sa copie. Refaire le test sur une réponse de
thread, puis après un redémarrage du bridge. Avec deux personnes utilisant le
même emoji, retirer une seule réaction doit conserver celle du bot distant ;
retirer la dernière doit l'enlever. Vérifier qu'aucun nouveau message n'apparaît.
