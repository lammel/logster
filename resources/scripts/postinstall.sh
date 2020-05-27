#!/bin/sh

id loghamster >/dev/null 2>&1
if [ $? -gt 0 ]; then
  echo "Creating local user 'loghamster'"
  useradd loghamster -c 'Loghamster log transporter' --system -M -N
fi
echo "Adjust permissions for config files"
chown -R loghamster /etc/loghamster/
chmod 640 /etc/loghamster/loghamster.conf
if [ -f /etc/init.d/loghamster ]; then
  echo "Adjust permission for systemV init script"
  chmod 755 /etc/init.d/loghamster
fi
