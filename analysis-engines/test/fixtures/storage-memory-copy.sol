pragma solidity ^0.8.30;

contract StorageCopyCases {
    struct Cell { uint128 amount; uint128 bonus; }
    struct Nested { Cell cell; uint128 extra; }
    struct Recipient { address payable payee; uint128 amount; }
    struct Flags { int128 signedAmount; uint64 amount; bool enabled; bytes4 tag; }
    enum Mode { Off, On }
    struct Tagged { uint128 amount; Mode mode; }
    struct Bag { uint128[] values; }
    Cell public cell;
    mapping(uint256 => Cell) public items;
    Nested private nested;
    Cell[2] public arrayCells;
    Recipient public recipient;
    Flags public flags;
    uint256 public keyCalls;
    mapping(bytes4 => Cell) private byteItems;
    Tagged public tagged;
    Bag private bag;

    constructor(address payable receiver) {
        cell = Cell(7, 5);
        items[1] = Cell(11, 3);
        items[2] = Cell(23, 4);
        nested = Nested(Cell(17, 3), 2);
        arrayCells[0] = Cell(31, 2);
        arrayCells[1] = Cell(41, 3);
        recipient = Recipient(receiver, 13);
        flags = Flags(-3, 11, true, bytes4(0x11223344));
        byteItems[0x11223344] = Cell(29, 3);
        tagged = Tagged(19, Mode.On);
        bag.values.push(3);
    }
    function readNested() external view returns (uint128, uint128, uint128) {
        return (nested.cell.amount, nested.cell.bonus, nested.extra);
    }
    function plainThenStore(uint256 x, address payable to) external {
        Cell memory a = cell; cell.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function memoryAlias(uint256 x, address payable to) external {
        Cell memory a = cell; Cell memory b = a; b.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function rebindMemory(uint256 x, address payable to) external {
        Cell memory a = cell; Cell memory b = a; a = items[2]; b.amount = uint128(x); to.transfer(a.amount + b.amount);
    }
    function storageAliasKey(uint256 x, address payable to) external {
        uint256 key = 1; Cell storage r = items[key]; key = 2; Cell memory a = r;
        r = items[2]; r.amount = uint128(x); a.amount = a.amount + a.bonus; to.transfer(a.amount);
    }
    function branchCopy(uint256 x, address payable to) external {
        Cell memory a; if (x % 2 == 0) a = cell; else a = items[1];
        cell.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function nestedCopy(uint256 x, address payable to) external {
        Nested memory a = nested; nested.cell.amount = uint128(x); a.cell.bonus = uint128(x);
        to.transfer(a.cell.amount + a.cell.bonus + a.extra);
    }
    function storeThenCopy(uint128 x, address payable to) external {
        cell.amount = x; Cell memory a = cell; to.transfer(a.amount + a.bonus);
    }
    function compoundThenCopy(uint128 x, address payable to) external {
        cell.bonus += x; Cell memory a = cell; to.transfer(a.amount + a.bonus);
    }
    function enumCopy(address payable to) external {
        Tagged memory a = tagged; tagged.amount = 1; to.transfer(a.amount);
    }
    function dynamicFieldCopy(address payable to) external {
        Bag memory b = bag; to.transfer(b.values.length);
    }
    function bagCopyOnly(address payable to) external {
        Bag memory b = bag; to.transfer(1);
    }
    function twoCopies(uint256 x, address payable to) external {
        Cell memory a = cell; cell.amount = uint128(x); Cell memory b = cell; to.transfer(a.amount + b.amount);
    }
    function mutate(Cell memory a, uint256 x) internal pure { a.amount = uint128(x); }
    function callMemory(uint256 x, address payable to) external {
        Cell memory a = cell; mutate(a, x); to.transfer(a.amount + a.bonus);
    }
    function mutateAndPay(Cell memory a, uint256 x, address payable to) internal {
        a.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function implicitMemoryArgument(uint256 x, address payable to) external { mutateAndPay(cell, x, to); }
    function copyValue() internal view returns (uint256) { Cell memory a = cell; return a.amount + a.bonus; }
    function readOnlySummaryCopy(uint256, address payable to) external { to.transfer(copyValue()); }
    function keyDivisionDiscarded(uint256 x, address payable to) external {
        Cell memory a = items[99 / x]; a.amount = 1; a.bonus = 2; to.transfer(a.amount + a.bonus);
    }
    function nextKey() internal returns (uint256) { keyCalls++; return 1; }
    function keyEffects(uint256 x, address payable to) external {
        Cell memory a = items[nextKey()]; a.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function dirtyAllocator(uint256 x, address payable to) external {
        assembly { mstore(0x40, add(0x80, and(x, 0x1f))) }
        Cell memory a = cell; to.transfer(a.amount + a.bonus);
    }
    function arrayElementCopy(uint256 x, address payable to) external {
        Cell memory a = arrayCells[x]; to.transfer(a.amount + a.bonus);
    }
    function copyThenRevert(uint256 x, address payable to) external {
        Cell memory a = cell; cell.amount = uint128(x); require(x != 0); to.transfer(a.amount + a.bonus);
    }
    function assignmentCopy(uint256 x, address payable to) external {
        Cell memory a = Cell(1, 2); Cell memory b = a; a = cell; b.amount = uint128(x); to.transfer(a.amount + b.amount);
    }
    function recipientSwap(uint256, address payable next) external {
        Recipient memory a = recipient; recipient.payee = next; a.payee.transfer(a.amount);
    }
    function typedFields(uint256 x, address payable to) external {
        Flags memory a = flags; flags.enabled = false; flags.signedAmount = int128(uint128(x));
        require(a.enabled && a.tag == bytes4(0x11223344));
        int128 total = a.signedAmount + int128(uint128(x)); require(total >= 0);
        to.transfer(uint256(uint128(total)) + a.amount);
    }
    function nestedIndependentCopies(uint256 x, address payable to) external {
        Nested memory a = nested; Nested memory b = nested; a.cell.amount = uint128(x); to.transfer(b.cell.amount + a.cell.amount);
    }
    function twoCopyKeyValues(uint256 x, address payable to) external {
        uint256 key = 1; Cell memory a = items[key]; key = 2; Cell memory b = items[key];
        key = 1; items[2].bonus = uint128(x); to.transfer(a.amount + b.bonus);
    }
    function conditionalReference(uint256 x, address payable to) external {
        Cell storage r = x == 0 ? cell : items[1]; Cell memory a = r;
        cell.amount = uint128(x); to.transfer(a.amount + a.bonus);
    }
    function byteKey(uint256, address payable to) external { Cell memory a = byteItems[0x11223344]; to.transfer(a.amount + a.bonus); }
}
