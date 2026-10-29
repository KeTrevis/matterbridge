# Déployer sur le serveur

Pour valider les changements avant déploiement, utiliser l'[environnement local de test](LOCAL_TEST.md).

Le code est publié sur [KeTrevis/matterbridge](https://github.com/KeTrevis/matterbridge),
branche `master`. Le serveur garde son clone dans
`/home/ketre/slack-discord-bridge/source`. Chaque déploiement y exécute
`git pull --ff-only origin master`.

Après avoir commité et poussé les changements, se connecter au serveur et lancer :

```sh
ssh ketre@82.64.155.44
cd ~/slack-discord-bridge/source
./scripts/update-server.sh
```

Le script refuse un clone modifié, une autre branche que `master` ou un autre
remote. Git et Docker Compose doivent être installés sur le serveur.

Installation initiale du clone, depuis le serveur :

```sh
cd ~/slack-discord-bridge
git clone --branch master https://github.com/KeTrevis/matterbridge.git source
cd source
./scripts/update-server.sh
```

Les tokens restent dans `/home/ketre/slack-discord-bridge/.env`, hors du clone
Git. La configuration `source/deploy/matterbridge.toml` est copiée vers
`matterbridge.toml` à chaque déploiement. Le Compose utilisé conserve le nom de
projet `slack-discord-bridge` et le même volume de données.

Les associations entre messages et threads sont conservées dans SQLite via
`[general].MessageStorePath`, dans le volume Docker
`slack-discord-bridge_bridge-data` (`/var/lib/matterbridge/message-links.sqlite3`
dans le conteneur). Ce volume survit aux redémarrages et aux déploiements.
La base contient les identifiants, comptes et salons, sans contenu des messages
ni tokens. Ne pas supprimer ce volume (`docker compose down -v`) pour conserver
les correspondances. Les associations déjà perdues avant l'activation de cette
base ne peuvent pas être reconstruites automatiquement ; la base `data/bridge.sqlite`
de l'ancien bridge reste intacte et n'est pas importée.

L'image est construite sur le serveur et les tests Slack/Discord/gateway du
Dockerfile doivent réussir avant le redémarrage. Le script conserve les anciens
fichiers Compose/configuration dans `.backups/` ; le code est récupéré depuis GitHub.
Il restaure les fichiers précédents et leur image si le
conteneur s'arrête ou redémarre pendant les 20 premières secondes.
Ce contrôle vérifie le processus ; le relais des messages se vérifie ensuite
dans Slack et Discord. Une coupure réseau pendant le remplacement peut
nécessiter une vérification manuelle.

Pour consulter l’état et les logs :

```sh
ssh ketre@82.64.155.44
cd ~/slack-discord-bridge
docker compose ps
docker compose logs --tail=100 bridge
```

Restauration manuelle (remplacer `RELEASE` par la sauvegarde voulue) :

```sh
cp .backups/RELEASE/compose.yaml compose.yaml
cp .backups/RELEASE/matterbridge.toml matterbridge.toml
docker compose up -d --no-build --pull never bridge
```

Les sauvegardes et images anciennes sont conservées pour permettre
le retour arrière. Leur nettoyage reste manuel.

## Nom et avatar des auteurs

Les messages texte et les fichiers affichent le nom et l’avatar de leur auteur, avec le suffixe
`[Slack]` ou `[Discord]`, y compris dans les threads. Le nom n’est plus ajouté
au début du texte. Cela concerne les nouveaux messages, sans renommer les anciens.

Le bot Discord doit avoir **Gérer les webhooks** dans le salon relié, ainsi que
les permissions de création et d’écriture dans les threads publics. La suppression
des messages de webhook via le bot nécessite **Gérer les messages**.
L’application Slack doit avoir le scope **chat:write.customize** ; si ce scope
vient d’être ajouté, réinstaller l’application dans le workspace pour l’accorder.
Le manifeste fourni contient déjà ce scope.

Les fichiers Slack utilisent `username` et `icon_url` sur
`files.completeUploadExternal`, avec le même scope `chat:write.customize` que
les messages texte. Le texte accompagnant un fichier est conservé sans ajouter
« File from… ». L’extension correspondante est incluse dans notre copie vendored
de `slack-go/slack/files.go` ; la conserver lors d’une régénération du vendor.

Sur Discord, les noms de webhook sont limités à 80 caractères ; les noms plus
longs sont tronqués sans couper les caractères UTF-8. Les avatars par défaut,
animés et propres au serveur sont pris en charge. Les messages déjà envoyés
conservent leur identité initiale ; ce changement s’applique aux nouveaux envois.
Les réponses gardent leur identité grâce à `UseNativeThreads = true` et
`AutoWebhooks = true`, activés dans les configurations fournies.

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
