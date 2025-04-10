VERSION=${VERSION:-"v0.50.10-helios-81"}

echo "Deploy Store"
git tag math/$VERSION
git push origin math/$VERSION
sleep 5
cd ./math
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/math@$VERSION
cd ..

echo "Deploy Store"
git tag store/$VERSION
git push origin store/$VERSION
sleep 5
cd ./store
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/store@$VERSION
cd ..

echo "Deploy x/circuit"
git tag x/circuit/$VERSION
git push origin x/circuit/$VERSION
sleep 5
cd ./x/circuit
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/circuit@$VERSION
cd ../..

echo "Deploy x/evidence"
git tag x/evidence/$VERSION
git push origin x/evidence/$VERSION
sleep 5
cd ./x/evidence
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/evidence@$VERSION
cd ../..

echo "Deploy x/feegrant"
git tag x/feegrant/$VERSION
git push origin x/feegrant/$VERSION
sleep 5
cd ./x/feegrant
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/feegrant@$VERSION
cd ../..

echo "Deploy x/nft"
git tag x/nft/$VERSION
git push origin x/nft/$VERSION
sleep 5
cd ./x/nft
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/nft@$VERSION
cd ../..

echo "Deploy x/tx"
git tag x/tx/$VERSION
git push origin x/tx/$VERSION
sleep 5
cd ./x/tx
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/tx@$VERSION
cd ../..

echo "Deploy x/upgrade"
git tag x/upgrade/$VERSION
git push origin x/upgrade/$VERSION
sleep 5
cd ./x/upgrade
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk/x/upgrade@$VERSION
cd ../..

echo "Update cosmos-sdk go.mod with the new dependencies"
go mod edit -replace cosmossdk.io/store=github.com/Helios-Chain-Labs/cosmos-sdk/store@$VERSION
go mod edit -replace cosmossdk.io/x/evidence=github.com/Helios-Chain-Labs/cosmos-sdk/x/evidence@$VERSION
go mod edit -replace cosmossdk.io/x/feegrant=github.com/Helios-Chain-Labs/cosmos-sdk/x/feegrant@$VERSION
go mod edit -replace cosmossdk.io/x/upgrade=github.com/Helios-Chain-Labs/cosmos-sdk/x/upgrade@$VERSION
go mod tidy

sleep 5

echo "Deploy cosmos-sdk"
git add .
git commit -m "Publish $VERSION"
git push
git tag $VERSION
git push origin $VERSION
GOPROXY=proxy.golang.org go list -m github.com/Helios-Chain-Labs/cosmos-sdk@$VERSION

echo "Publish done"
